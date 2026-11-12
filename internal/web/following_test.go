package web

import (
	"bufio"
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
func TestBrowserWheel(t *testing.T) { runBrowserFixture(t, "wheel.cjs", "short-first") }
func TestBrowserPerformance(t *testing.T) {
	if os.Getenv("KEEPSESS_PERF_DIR") == "" {
		t.Skip("set KEEPSESS_PERF_DIR to collect headed browser traces")
	}
	runBrowserFixture(t, "performance.cjs", "short-first")
}
func TestBrowserAppearance(t *testing.T) { runBrowserFixture(t, "appearance.cjs", "short-first") }
func TestBrowserAppearanceFallback(t *testing.T) {
	t.Setenv("KEEPSESS_HIGHLIGHT_FALLBACK", "1")
	runBrowserFixture(t, "appearance.cjs", "short-first")
}
func TestBrowserFollowing(t *testing.T) { runBrowserFixture(t, "following.cjs", "first") }
func TestBrowserDenseHighlight(t *testing.T) {
	runBrowserFixture(t, "dense-highlight.cjs", "short-first")
}
func TestBrowserDenseHighlightDOM(t *testing.T) {
	t.Setenv("KEEPSESS_DENSE_DOM", "1")
	runBrowserFixture(t, "dense-highlight.cjs", "short-first")
}
func TestBrowserGutter(t *testing.T)    { runBrowserFixture(t, "gutter.cjs", "demo-health") }
func TestBrowserSearch(t *testing.T)    { runBrowserFixture(t, "search.cjs", "demo-health") }
func TestBrowserWorkspace(t *testing.T) { runBrowserFixture(t, "workspace.cjs", "demo-health") }
func TestBrowserOverview(t *testing.T)  { runBrowserFixture(t, "overview.cjs", "short-first") }
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
	addr, _ := testutil.SSHWithExec(t, func(command string, ch ssh.Channel, requests <-chan *ssh.Request) {
		payload := testutil.LoginPayload(command)
		name := payload[strings.LastIndex(payload, "\n")+1:]
		if name == "exec bash -il" {
			lines := make(chan string, 1)
			go func() {
				reader := bufio.NewReader(ch)
				var line strings.Builder
				for {
					b, err := reader.ReadByte()
					if err != nil {
						return
					}
					if b == '\r' || b == '\n' {
						select {
						case lines <- line.String():
							line.Reset()
						case <-stop:
							return
						}
					} else {
						line.WriteByte(b)
					}
				}
			}()
			_, _ = io.WriteString(ch, "\x1b[32mdemo@lab\x1b[0m:~$ ")
			for {
				select {
				case line := <-lines:
					_, _ = io.WriteString(ch, line+"\r\n")
					if strings.HasPrefix(line, "echo ") {
						_, _ = io.WriteString(ch, strings.TrimPrefix(line, "echo ")+"\r\n")
					}
					if line == "pwd" {
						_, _ = io.WriteString(ch, "/home/demo\r\n")
					}
					_, _ = io.WriteString(ch, "\x1b[32mdemo@lab\x1b[0m:~$ ")
				case req, ok := <-requests:
					if !ok {
						return
					}
					if req.Type == "signal" {
						_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
						return
					}
				case <-stop:
					return
				}
			}
		}

		switch name {
		case "systemctl status keepsess":
			_, _ = io.WriteString(ch, "● keepsess.service — Persistent session daemon\n    Loaded: loaded (/etc/systemd/system/keepsess.service; enabled)\n    Active: \x1b[32mactive (running)\x1b[0m since Thu 2026-11-12 09:41:03 SGT\n  Main PID: 2418 (keepsess)\n     Tasks: 12     Memory: 18.4M\n\nNov 12 09:41:03 lab keepsess[2418]: daemon ready · 3 sessions online\nNov 12 09:41:04 lab keepsess[2418]: SSH transport healthy\n")
			_, _ = io.WriteString(ch.Stderr(), "WARNING: history capacity is unlimited; check available disk space\n")
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
		command := name
		if name == "demo-health" {
			command = "systemctl status keepsess"
		}
		return m.Operate(context.Background(), "exec", core.Request{Session: session.Session, Name: name, Command: command, Cwd: "/srv/keepsess", Interactive: pty, PTY: pty, CollectOutput: &no})
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
