package web

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/history"
	"github.com/zytyan/keepsess/internal/testutil"
)

func setup(t *testing.T, o Options) (*Server, *core.Manager, *history.Store) {
	t.Helper()
	m := core.NewManager()
	st, e := history.Open(t.TempDir(), m.InstanceID(), 0)
	if e != nil {
		t.Fatal(e)
	}
	m.SetEventSink(st)
	s, e := Start(context.Background(), m, st, o)
	if e != nil {
		st.Close()
		t.Fatal(e)
	}
	t.Cleanup(func() { m.CloseAll(); s.Close(); st.Close() })
	return s, m, st
}
func connectSSH(t *testing.T, m *core.Manager, mode string) string {
	t.Helper()
	addr, _ := testutil.SSH(t)
	h, p, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(p)
	r, e := m.Connect(context.Background(), core.Options{Host: h, Port: port, Protocol: "ssh", Mode: mode, Username: "user", Password: "secret"})
	if e != nil {
		t.Fatal(e)
	}
	return r.Session
}
func dial(t *testing.T, s *Server, session string, after string) *websocket.Conn {
	t.Helper()
	u, _ := url.Parse(s.Info().URL)
	origin := u.Scheme + "://" + u.Host
	u.Scheme = "ws"
	u.Path += "ws"
	q := u.Query()
	q.Set("session", session)
	q.Set("after", after)
	u.RawQuery = q.Encode()
	c, _, e := websocket.DefaultDialer.Dial(u.String(), http.Header{"Origin": []string{origin}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func readType(t *testing.T, c *websocket.Conn, typ string) map[string]json.RawMessage {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 200; i++ {
		var m map[string]json.RawMessage
		if e := c.ReadJSON(&m); e != nil {
			t.Fatal(e)
		}
		var kind string
		_ = json.Unmarshal(m["type"], &kind)
		if kind == typ {
			return m
		}
	}
	t.Fatal("message missing", typ)
	return nil
}
func TestHTTPPathsOriginHostAndAutomaticPorts(t *testing.T) {
	occupied, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer occupied.Close()
	s, _, _ := setup(t, Options{Listen: occupied.Addr().String(), BasePath: "/nested/keepsess/", PublicURL: "https://terminal.example/console/"})
	if s.Info().Listen == occupied.Addr().String() || s.Info().URL != "https://terminal.example/console/" {
		t.Fatal(s.Info())
	}
	base := "http://" + s.Info().Listen + "/nested/keepsess/"
	for _, p := range []string{"", "app.js", "vendor/xterm.js", "api/info", "api/instances"} {
		r, e := http.Get(base + p)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if r.StatusCode != 200 || len(b) == 0 {
			t.Fatal(p, r.StatusCode)
		}
	}
	for _, tc := range []struct {
		host, origin string
		want         int
	}{{"evil.example", "", 403}, {"", "https://evil.example", 403}, {"terminal.example", "https://terminal.example", 200}} {
		req, _ := http.NewRequest("GET", base+"api/info", nil)
		if tc.host != "" {
			req.Host = tc.host
		}
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		_ = r.Body.Close()
		if r.StatusCode != tc.want {
			t.Fatal(tc, r.StatusCode)
		}
	}
}
func TestWebDefaultReadonlyUnlockExecAndCoreReadonly(t *testing.T) {
	s, m, _ := setup(t, Options{Listen: "127.0.0.1:0"})
	id := connectSSH(t, m, "exec")
	c := dial(t, s, id, "0")
	readType(t, c, "hello")
	_ = c.WriteJSON(request{Type: "exec", ID: "blocked", Session: id, Command: "both"})
	r := readType(t, c, "receipt")
	if !strings.Contains(string(r["error"]), "read-only") {
		t.Fatal(r)
	}
	tasks, _ := m.TaskList(id)
	if len(tasks) != 0 {
		t.Fatal("readonly request sent", tasks)
	}
	_ = c.WriteJSON(request{Type: "unlock", Enabled: true})
	readType(t, c, "lock")
	_ = c.WriteJSON(request{Type: "exec", ID: "run", Session: id, Command: "both"})
	r = readType(t, c, "receipt")
	var result core.Result
	_ = json.Unmarshal(r["result"], &result)
	if result.InputState != "sent" || result.Handle == "" {
		t.Fatal(string(r["result"]))
	}
	// Web observation must not advance the MCP task output cursor.
	v, e := m.Operate(context.Background(), "task_wait", core.Request{Handle: result.Handle})
	if e != nil || !strings.Contains(v.Output, "stdout") || !strings.Contains(v.Output, "stderr") {
		t.Fatal(v, e)
	}
	_, e = m.Configure(id, core.ConfigPatch{"read_only": json.RawMessage(`true`)})
	if e != nil {
		t.Fatal(e)
	}
	_ = c.WriteJSON(request{Type: "exec", ID: "protected", Session: id, Command: "both"})
	r = readType(t, c, "receipt")
	_ = json.Unmarshal(r["result"], &result)
	if result.InputState != "not_sent" || !strings.Contains(result.Error, "read_only") {
		t.Fatal(result)
	}
	_ = c.Close()
	again := dial(t, s, id, "0")
	readType(t, again, "hello")
	_ = again.WriteJSON(request{Type: "exec", Session: id, Command: "both"})
	r = readType(t, again, "receipt")
	if !strings.Contains(string(r["error"]), "read-only") {
		t.Fatal(r)
	}
}
func TestWebReplayCloseAndNewEventsHaveUniqueSequences(t *testing.T) {
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0"})
	id := connectSSH(t, m, "exec")
	_, e := m.Operate(context.Background(), "exec", core.Request{Session: id, Command: "both"})
	if e != nil {
		t.Fatal(e)
	}
	_, _ = m.RenameSession(id, "prod")
	_, _ = m.Close(id)
	c := dial(t, s, id, "0")
	readType(t, c, "hello")
	last := uint64(0)
	found := false
	for i := 0; i < 100; i++ {
		r := readType(t, c, "event")
		var e core.Event
		_ = json.Unmarshal(r["event"], &e)
		if e.Seq <= last {
			t.Fatal("duplicate or reordered", last, e.Seq)
		}
		last = e.Seq
		if e.Disposed && e.Handle == "" && e.Name == "prod" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing tombstone")
	}
	_ = c.Close()
	st.Emit(core.Event{Kind: "session", Session: "s2", Name: "new"})
	c = dial(t, s, id, strconv.FormatUint(last, 10))
	readType(t, c, "hello")
	r := readType(t, c, "event")
	var event core.Event
	_ = json.Unmarshal(r["event"], &event)
	if event.Seq <= last {
		t.Fatal(event)
	}
}
func TestWebRejectsCrossOriginWebsocketAndUnsupportedOperations(t *testing.T) {
	s, _, _ := setup(t, Options{Listen: "127.0.0.1:0"})
	u := strings.Replace(s.Info().URL, "http:", "ws:", 1) + "ws?session=s1"
	for _, origin := range []string{"", "http://evil.example"} {
		_, resp, e := websocket.DefaultDialer.Dial(u, http.Header{"Origin": []string{origin}})
		if e == nil || resp.StatusCode != 403 {
			t.Fatal(e, resp)
		}
	}
}

func TestTLSReverseProxyPreservedAndStrippedPrefixes(t *testing.T) {
	for _, strip := range []bool{false, true} {
		t.Run(strconv.FormatBool(strip), func(t *testing.T) {
			internal := "/console/"
			if strip {
				internal = "/"
			}
			s, _, _ := setup(t, Options{Listen: "127.0.0.1:0", BasePath: internal, PublicURL: "https://terminal.example/console/"})
			target, _ := url.Parse("http://" + s.Info().Listen)
			proxy := httputil.NewSingleHostReverseProxy(target)
			director := proxy.Director
			proxy.Director = func(r *http.Request) {
				director(r)
				if strip {
					r.URL.Path = strings.TrimPrefix(r.URL.Path, "/console")
				}
			}
			tlsServer := httptest.NewTLSServer(proxy)
			defer tlsServer.Close()
			req, _ := http.NewRequest("GET", tlsServer.URL+"/console/api/info", nil)
			req.Host = "terminal.example"
			req.Header.Set("Origin", "https://terminal.example")
			resp, err := tlsServer.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatal(resp.StatusCode)
			}
			d := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, HandshakeTimeout: time.Second}
			c, _, err := d.Dial(strings.Replace(tlsServer.URL, "https:", "wss:", 1)+"/console/ws?session=s1", http.Header{"Origin": []string{"https://terminal.example"}, "Host": []string{"terminal.example"}})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			readType(t, c, "hello")
		})
	}
}

func TestInvalidBasePathReturnsErrorWithoutPanicking(t *testing.T) {
	m := core.NewManager()
	st, err := history.Open(t.TempDir(), m.InstanceID(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, base := range []string{"//", "/../root/", "/{wildcard}/", "/space root/", "/bad%escape/", "/query?/"} {
		if s, err := Start(context.Background(), m, st, Options{Listen: "127.0.0.1:0", BasePath: base}); err == nil {
			s.Close()
			t.Fatal("accepted invalid base path", base)
		}
	}
}

func TestWebRejectsPreviousInstanceBeforeUpgrade(t *testing.T) {
	s, _, _ := setup(t, Options{Listen: "127.0.0.1:0"})
	u, _ := url.Parse(s.Info().URL)
	origin := u.Scheme + "://" + u.Host
	u.Scheme = "ws"
	u.Path += "ws"
	u.RawQuery = "session=s1&instance=previous-process"
	_, r, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Origin": []string{origin}})
	if err == nil || r == nil || r.StatusCode != http.StatusConflict {
		t.Fatal(err, r)
	}
}
