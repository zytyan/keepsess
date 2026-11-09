package web

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/settings"
	"github.com/zytyan/keepsess/internal/testutil"
	"golang.org/x/crypto/ssh"
)

// Real SSH output and commands drive the browser; the control endpoint belongs
// only to this optional test fixture, never to the production Web server.
func TestBrowserWheel(t *testing.T)      { runBrowserFixture(t, "wheel.cjs", "short-first") }
func TestBrowserAppearance(t *testing.T) { runBrowserFixture(t, "appearance.cjs", "short-first") }
func TestBrowserFollowing(t *testing.T)  { runBrowserFixture(t, "following.cjs", "first") }
func TestBrowserOverview(t *testing.T)   { runBrowserFixture(t, "overview.cjs", "short-first") }
func runBrowserFixture(t *testing.T, script, initial string) {
	if os.Getenv("KEEPSESS_BROWSER_TEST") != "1" {
		t.Skip("set KEEPSESS_BROWSER_TEST=1 with Node and Playwright installed")
	}
	stop := make(chan struct{})
	defer close(stop)
	var mu sync.Mutex
	streams := make(map[string]chan string)
	stream := func(name string) chan string {
		mu.Lock()
		defer mu.Unlock()
		if streams[name] == nil {
			streams[name] = make(chan string, 4)
		}
		return streams[name]
	}
	addr, _ := testutil.SSHWithExec(t, func(command string, ch ssh.Channel, _ <-chan *ssh.Request) {
		payload := testutil.LoginPayload(command)
		name := payload[strings.LastIndex(payload, "\n")+1:]
		switch name {
		case "pwd":
			_, _ = io.WriteString(ch, "/srv/keepsess\n")
		case "hostname":
			_, _ = io.WriteString(ch, "demo-server\n")
		case "uptime":
			_, _ = io.WriteString(ch, "up 3 days, load average: 0.04, 0.02, 0.01\n")
		default:
			count := 100
			if strings.HasPrefix(name, "short-") || strings.HasPrefix(name, "done-") {
				count = 2
			}
			for i := 0; i < count; i++ {
				_, _ = fmt.Fprintf(ch, "%s initial line %03d\n", name, i)
			}
		}
		if name == "pwd" || name == "hostname" || name == "uptime" || strings.HasPrefix(name, "done-") {
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
			return
		}
		for {
			select {
			case text := <-stream(name):
				if _, err := io.WriteString(ch, text); err != nil {
					return
				}
			case <-stop:
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
				return
			}
		}
	})
	home := t.TempDir()
	if _, err := settings.LoadHome(home); err != nil {
		t.Fatal(err)
	}
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0", SettingsHome: home})
	host, p, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(p)
	session, err := m.Connect(context.Background(), core.Options{Host: host, Port: port, Protocol: "ssh", Mode: "exec", Username: "demo", Password: "simulator", Name: "parallel-demo"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string, pty bool) (core.Result, error) {
		stream(name)
		no := false
		return m.Operate(context.Background(), "exec", core.Request{Session: session.Session, Name: name, Command: name, Cwd: "/srv/keepsess", Interactive: pty, PTY: pty, CollectOutput: &no})
	}
	first, err := create(initial, false)
	if err != nil || first.Handle == "" {
		t.Fatal(first, err)
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		name := r.URL.Query().Get("name")
		switch r.URL.Path {
		case "/create":
			v, e := create(name, r.URL.Query().Get("pty") == "true")
			if e != nil || v.Error != "" {
				http.Error(w, fmt.Sprint(v, e), 400)
				return
			}
			_ = json.NewEncoder(w).Encode(v)
		case "/bytes":
			b, e := hex.DecodeString(r.URL.Query().Get("hex"))
			if e != nil || len(b) > 1024 {
				http.Error(w, "invalid bytes", 400)
				return
			}
			select {
			case stream(name) <- string(b):
				_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			case <-time.After(time.Second):
				http.Error(w, "fixture backpressure", 503)
			}
		case "/append":
			count, _ := strconv.Atoi(r.URL.Query().Get("lines"))
			if count < 1 || count > 200 {
				http.Error(w, "invalid count", 400)
				return
			}
			var b strings.Builder
			for i := 0; i < count; i++ {
				fmt.Fprintf(&b, "%s %03d\n", r.URL.Query().Get("tag"), i)
			}
			select {
			case stream(name) <- b.String():
				_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			case <-time.After(time.Second):
				http.Error(w, "fixture backpressure", 503)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()
	until := time.Now().Add(3 * time.Second)
	for st.Status().WrittenSeq < st.Status().LastSeq && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	node := os.Getenv("KEEPSESS_NODE")
	if node == "" {
		node = "node"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/"+script, s.Info().URL, control.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser fixture: %v\n%s", err, out)
	}
	t.Log(string(out))
}
