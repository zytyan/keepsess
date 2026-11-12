package web

import (
	"context"
	"net"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/history"
	"github.com/zytyan/keepsess/internal/testutil"
)

// Optional real-browser acceptance. Go builds and normal tests require neither
// Node nor Playwright; opt in with KEEPSESS_BROWSER_TEST=1.
func TestBrowserAcceptance(t *testing.T) { runBrowserAcceptance(t, false) }
func TestBrowserIPProxy(t *testing.T)    { runBrowserAcceptance(t, true) }
func runBrowserAcceptance(t *testing.T, proxy bool) {
	if os.Getenv("KEEPSESS_BROWSER_TEST") != "1" {
		t.Skip("set KEEPSESS_BROWSER_TEST=1 with Node and Playwright installed")
	}
	root := t.TempDir()
	old, err := history.Open(root, "archived", 0)
	if err != nil {
		t.Fatal(err)
	}
	old.Emit(core.Event{Kind: "session", Session: "s1", Name: "archived-console", Mode: "stream", Protocol: "ssh", Status: core.Result{Connected: true}})
	old.Emit(core.Event{Kind: "output", Session: "s1", Data: []byte("archive retained\r\n"), Encoding: "utf-8"})
	old.Emit(core.Event{Kind: "output", Session: "s1", Data: []byte{0xd6}, Encoding: "gbk"})
	old.Emit(core.Event{Kind: "output", Session: "s1", Data: []byte{0xd0, 0xce, 0xc4}, Encoding: "gbk"})
	old.Close()
	m := core.NewManager()
	st, err := history.Open(root, m.InstanceID(), 0)
	if err != nil {
		t.Fatal(err)
	}
	m.SetEventSink(st)
	s, err := Start(context.Background(), m, st, Options{Listen: "127.0.0.1:0", BasePath: "/nested/keepsess/"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.CloseAll(); s.Close(); st.Close() })
	id := connectSSH(t, m, "exec")
	_, _ = m.RenameSession(id, "production")
	_, err = m.Operate(context.Background(), "exec", core.Request{Session: id, Command: "both"})
	if err != nil {
		t.Fatal(err)
	}
	closed := connectSSH(t, m, "exec")
	_, _ = m.RenameSession(closed, "closed-session")
	_, _ = m.Close(closed)
	stream := connectSSH(t, m, "stream")
	_, _ = m.RenameSession(stream, "console")
	failedAddr := testutil.Listen(t, func(raw net.Conn) { _ = raw.Close() })
	failedHost, failedPort, _ := net.SplitHostPort(failedAddr)
	failedN, _ := strconv.Atoi(failedPort)
	if _, err = m.Connect(context.Background(), core.Options{Host: failedHost, Port: failedN, Protocol: "ssh", Mode: "exec", Username: "user", Password: "secret", Name: "failed-connect"}); err == nil {
		t.Fatal("fixture connection unexpectedly succeeded")
	}
	telnetAddr := testutil.TelnetSwitch(t)
	telnetHost, telnetPort, _ := net.SplitHostPort(telnetAddr)
	telnetN, _ := strconv.Atoi(telnetPort)
	if _, err = m.Connect(context.Background(), core.Options{Host: telnetHost, Port: telnetN, Protocol: "telnet", Mode: "stream", Name: "telnet-console"}); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(3 * time.Second)
	for st.Status().WrittenSeq < st.Status().LastSeq && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	node := os.Getenv("KEEPSESS_NODE")
	if node == "" {
		node = "node"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	entry := s.Info().URL
	if proxy {
		target, _ := url.Parse("http://" + s.Info().Listen)
		front := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(target))
		defer front.Close()
		entry = front.URL + s.Info().BasePath
	}
	cmd := exec.CommandContext(ctx, node, "testdata/browser.cjs", entry)
	if proxy {
		cmd.Env = append(os.Environ(), "KEEPSESS_BROWSER_INSECURE_TLS_TEST=1")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser: %v\n%s", err, out)
	}
	t.Log(string(out))
}
