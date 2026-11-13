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
	"github.com/zytyan/keepsess/internal/telnet"
	"github.com/zytyan/keepsess/internal/testutil"
)

// Real SSH/Telnet streams exercise the retained xterm parser and incremental
// history cursor, including switches while an output write is still pending.
func TestBrowserStreamHistory(t *testing.T) {
	if os.Getenv("KEEPSESS_BROWSER_TEST") != "1" {
		t.Skip("set KEEPSESS_BROWSER_TEST=1")
	}
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0", BasePath: "/stream-history/"})
	type chunk struct {
		data []byte
		done chan error
	}
	streams := map[string]chan chunk{"ssh": make(chan chunk), "telnet": make(chan chunk)}
	stop := make(chan struct{})
	defer close(stop)
	var inputMu sync.Mutex
	inputs := map[string]string{}
	shell := func(protocol string, rw io.ReadWriter) {
		go func() {
			buf := make([]byte, 1024)
			for {
				n, err := rw.Read(buf)
				if n > 0 {
					inputMu.Lock()
					inputs[protocol] += string(buf[:n])
					inputMu.Unlock()
				}
				if err != nil {
					return
				}
			}
		}()
		var initial strings.Builder
		for i := 0; i < 4000; i++ {
			fmt.Fprintf(&initial, "%s-history-%04d %s\r\n", protocol, i, strings.Repeat("data ", 12))
		}
		if _, err := io.WriteString(rw, initial.String()); err != nil {
			return
		}
		for {
			select {
			case c := <-streams[protocol]:
				_, err := rw.Write(c.data)
				c.done <- err
				if err != nil {
					return
				}
			case <-stop:
				return
			}
		}
	}
	sshAddr, _ := testutil.SSHWithShell(t, func(rw io.ReadWriter) { shell("ssh", rw) })
	telnetAddr := testutil.Listen(t, func(c net.Conn) {
		for _, b := range []byte{255, 251, 1} {
			if _, err := c.Write([]byte{b}); err != nil {
				return
			}
		}
		shell("telnet", telnet.NewConn(c, 10*time.Second))
	})
	for _, target := range []struct{ protocol, addr string }{{"ssh", sshAddr}, {"telnet", telnetAddr}} {
		host, port, _ := net.SplitHostPort(target.addr)
		n, _ := strconv.Atoi(port)
		if _, err := m.Connect(context.Background(), core.Options{Protocol: target.protocol, Host: host, Port: n, Mode: "stream", Username: "user", Password: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("op") == "stats" {
			inputMu.Lock()
			defer inputMu.Unlock()
			_ = json.NewEncoder(w).Encode(inputs)
			return
		}
		stream, ok := streams[r.URL.Query().Get("protocol")]
		if !ok {
			http.Error(w, "unknown protocol", 400)
			return
		}
		data, err := hex.DecodeString(r.URL.Query().Get("hex"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		c := chunk{data: data, done: make(chan error, 1)}
		select {
		case stream <- c:
		case <-r.Context().Done():
			return
		}
		select {
		case err = <-c.done:
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer control.Close()
	waitRecorded(t, st)
	node := os.Getenv("KEEPSESS_NODE")
	if node == "" {
		node = "node"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "testdata/stream-history.cjs", s.Info().URL, control.URL).CombinedOutput()
	if err != nil {
		t.Fatalf("stream history browser: %v\n%s", err, out)
	}
	t.Log(string(out))
}
