package daemon_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/daemon"
	"github.com/zytyan/keepsess/internal/history"
	"github.com/zytyan/keepsess/internal/mcp"
	"github.com/zytyan/keepsess/internal/settings"
	"github.com/zytyan/keepsess/internal/testutil"
)

func TestMain(m *testing.M) {
	if home := os.Getenv("KEEPSESS_TEST_AGENT_HOME"); home != "" {
		cfg, err := settings.LoadHome(home)
		if err != nil {
			panic(err)
		}
		r, err := daemon.ReadRuntime(cfg)
		if err != nil {
			panic(err)
		}
		manager := core.NewManager()
		store, err := history.Open(cfg.History.Dir, manager.InstanceID(), 0)
		if err != nil {
			panic(err)
		}
		manager.SetEventSink(store)
		client := daemon.Connect(context.Background(), cfg, manager, store, r, func(settings.Config) error { return fmt.Errorf("test does not auto-start") })
		s := &mcp.Server{Manager: manager, WebInfo: func() any { return client.Info() }}
		err = s.Serve(context.Background(), os.Stdin, os.Stdout)
		manager.CloseAll()
		client.Close()
		store.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func eventually(t *testing.T, check func() bool) {
	t.Helper()
	until := time.Now().Add(6 * time.Second)
	for time.Now().Before(until) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
func config(t *testing.T) settings.Config {
	t.Helper()
	c, err := settings.LoadHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.Web.Listen = "127.0.0.1:0"
	return c
}
func start(t *testing.T, c settings.Config) (daemon.Runtime, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, c) }()
	var r daemon.Runtime
	eventually(t, func() bool {
		var err error
		r, err = daemon.ReadRuntime(c)
		if err != nil {
			return false
		}
		_, err = daemon.Probe(ctx, r)
		return err == nil
	})
	return r, cancel, done
}
func fetch(t *testing.T, base, endpoint string, v any) {
	t.Helper()
	r, err := http.Get(base + endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		b, _ := io.ReadAll(r.Body)
		t.Fatal(r.Status, string(b))
	}
	if err = json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

type agent struct {
	in   io.WriteCloser
	scan *bufio.Scanner
	cmd  *exec.Cmd
	next int
	log  bytes.Buffer
	once sync.Once
}

func newAgent(t *testing.T, c settings.Config) *agent {
	t.Helper()
	a := &agent{}
	a.cmd = exec.Command(os.Args[0])
	a.cmd.Env = append(os.Environ(), "KEEPSESS_TEST_AGENT_HOME="+c.Home)
	a.cmd.Stderr = &a.log
	var err error
	a.in, err = a.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := a.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	a.scan = bufio.NewScanner(out)
	a.scan.Buffer(make([]byte, 4096), 2<<20)
	if err = a.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.close(t) })
	return a
}
func (a *agent) close(t *testing.T) {
	a.once.Do(func() {
		a.in.Close()
		done := make(chan error, 1)
		go func() { done <- a.cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("agent: %v %s", err, a.log.String())
			}
		case <-time.After(5 * time.Second):
			a.cmd.Process.Kill()
			<-done
			t.Error("agent did not exit")
		}
	})
}
func (a *agent) call(t *testing.T, name string, args any) map[string]any {
	t.Helper()
	a.next++
	id := a.next
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	if _, err := fmt.Fprintln(a.in, string(b)); err != nil {
		t.Fatal(err)
	}
	response := make(chan []byte, 1)
	go func() {
		if a.scan.Scan() {
			response <- append([]byte(nil), a.scan.Bytes()...)
		} else {
			response <- nil
		}
	}()
	select {
	case b := <-response:
		var m struct {
			Result struct {
				Content map[string]any `json:"structuredContent"`
				IsError bool           `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err, string(b))
		}
		if m.Result.IsError {
			t.Fatal(name, m.Result.Content)
		}
		return m.Result.Content
	case <-time.After(8 * time.Second):
		t.Fatal("agent RPC timeout", name)
		return nil
	}
}
func TestSharedDaemonMultipleAgentsRestartRoutingAndArchive(t *testing.T) {
	c := config(t)
	r, cancel, done := start(t, c)
	defer func() { cancel(); <-done }()
	a, b := newAgent(t, c), newAgent(t, c)
	addr, _ := testutil.SSH(t)
	host, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	opts := map[string]any{"protocol": "ssh", "mode": "exec", "host": host, "port": n, "username": "user", "password": "secret"}
	one := a.call(t, "sess_connect", opts)
	two := b.call(t, "sess_connect", opts)
	id1, id2 := one["instance_id"].(string), two["instance_id"].(string)
	if id1 == id2 || one["session"] != "s1" || two["session"] != "s1" || !strings.Contains(one["comment"].(string), "instance_name") {
		t.Fatal(one, two)
	}
	a.call(t, "sess_configure", map[string]any{"instance_name": "部署检查 A"})
	b.call(t, "sess_configure", map[string]any{"instance_name": "网络诊断 B"})
	var instances []history.Instance
	eventually(t, func() bool {
		fetch(t, r.Info.URL, "api/instances", &instances)
		return len(instances) == 2 && instances[0].Current && instances[1].Current && instances[0].Name != "" && instances[1].Name != ""
	})
	u, _ := url.Parse(r.Info.URL + "ws?instance=" + id1 + "&session=s1")
	origin := u.Scheme + "://" + u.Host
	u.Scheme = "ws"
	ws, _, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Origin": []string{origin}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	read := func(typ string) map[string]any {
		t.Helper()
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			var m map[string]any
			if err := ws.ReadJSON(&m); err != nil {
				t.Fatal(err)
			}
			if m["type"] == typ {
				return m
			}
		}
	}
	read("hello")
	_ = ws.WriteJSON(map[string]any{"type": "exec", "id": "blocked", "session": "s1", "command": "both"})
	if m := read("receipt"); !strings.Contains(m["error"].(string), "read-only") {
		t.Fatal(m)
	}
	_ = ws.WriteJSON(map[string]any{"type": "unlock", "enabled": true})
	read("lock")
	_ = ws.WriteJSON(map[string]any{"type": "exec", "id": "only-first", "session": "s1", "command": "both"})
	receipt := read("receipt")
	if receipt["error"] != nil || receipt["result"].(map[string]any)["input_state"] != "sent" {
		t.Fatal(receipt)
	}
	first := a.call(t, "sess_task_list", map[string]any{})
	second := b.call(t, "sess_task_list", map[string]any{})
	if len(first["tasks"].([]any)) != 1 || len(second["tasks"].([]any)) != 0 {
		t.Fatal("operation crossed instance", first, second)
	}
	if os.Getenv("KEEPSESS_BROWSER_TEST") == "1" {
		node := os.Getenv("KEEPSESS_NODE")
		if node == "" {
			node = "node"
		}
		ctx, cancelBrowser := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := exec.CommandContext(ctx, node, "testdata/browser.cjs", r.Info.URL, id1, id2).CombinedOutput()
		cancelBrowser()
		if err != nil {
			t.Fatalf("browser: %v\n%s", err, out)
		}
		t.Log(string(out))
	}
	// The daemon owns no SSH connection: restarting it preserves the same live
	// Agent instance/session/task and permits subsequent explicit commands.
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	r, cancel, done = start(t, c)
	eventually(t, func() bool {
		fetch(t, r.Info.URL, "api/instances", &instances)
		return len(instances) == 2 && instances[0].Current && instances[1].Current
	})
	continued := a.call(t, "sess_exec", map[string]any{"session": "s1", "command": "both"})
	if continued["exit_code"] != float64(0) {
		t.Fatal(continued)
	}
	a.close(t)
	eventually(t, func() bool {
		fetch(t, r.Info.URL, "api/instances", &instances)
		for _, i := range instances {
			if i.ID == id1 {
				return !i.Current && i.Name == "部署检查 A"
			}
		}
		return false
	})
	var catalog struct {
		Records  []history.Record `json:"records"`
		Indexing bool             `json:"indexing"`
	}
	eventually(t, func() bool {
		fetch(t, r.Info.URL, "api/catalog?instance="+id1, &catalog)
		return !catalog.Indexing && len(catalog.Records) > 0
	})
	for _, rec := range catalog.Records {
		if !rec.Event.Disposed || rec.Event.Status.Connected {
			t.Fatal("offline archive writable/live", rec)
		}
	}
	b.close(t)
	eventually(t, func() bool {
		fetch(t, r.Info.URL, "api/instances", &instances)
		for _, i := range instances {
			if i.Current {
				return false
			}
		}
		return true
	})
	if info, err := daemon.Probe(context.Background(), r); err != nil || !info.Enabled {
		t.Fatal("daemon exited with last Agent", info, err)
	}
	// No daemon token is exposed in public discovery or the runtime's info view.
	t.Logf("two OS processes shared %s; routed read-only/unlocked operations; daemon restart retained %s and %s; name/history survived Agent exit", r.Info.URL, id1, id2)
}
func TestConcurrentStartersAndPrivateIPC(t *testing.T) {
	c := config(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	launch := func(c settings.Config) error {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := daemon.Run(ctx, c); err != nil {
				t.Error(err)
			}
		}()
		return nil
	}
	const count = 8
	results := make(chan daemon.Runtime, count)
	var callers sync.WaitGroup
	for i := 0; i < count; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			r, err := daemon.Ensure(ctx, c, launch)
			if err != nil {
				t.Error(err)
				return
			}
			results <- r
		}()
	}
	callers.Wait()
	close(results)
	pid := 0
	endpoint := ""
	for r := range results {
		if endpoint != "" && (endpoint != r.Endpoint || pid != r.PID) {
			t.Fatal("duplicate daemon", r)
		}
		endpoint = r.Endpoint
		pid = r.PID
	}
	if endpoint == "" {
		t.Fatal("no daemon")
	}
	response, err := http.Get(endpoint + "info")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("IPC without token", response.StatusCode)
	}
	st, err := os.Stat(filepath.Join(c.RuntimeDir(), "daemon.json"))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, err)
	}
	cancel()
	wg.Wait()
}
