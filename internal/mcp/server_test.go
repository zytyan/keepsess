package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/testutil"
)

type client struct {
	in       *io.PipeWriter
	messages chan map[string]any
	pending  map[string]map[string]any
	done     chan error
}

func harness(t *testing.T) *client {
	t.Helper()
	r, w := io.Pipe()
	rr, ww := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	c := &client{in: w, messages: make(chan map[string]any, 100), pending: make(map[string]map[string]any), done: make(chan error, 1)}
	s := &Server{Manager: core.NewManager()}
	go func() { c.done <- s.Serve(ctx, r, ww); _ = ww.Close() }()
	go func() {
		defer close(c.messages)
		scan := bufio.NewScanner(rr)
		scan.Buffer(make([]byte, 4096), 1<<20)
		for scan.Scan() {
			var v map[string]any
			if json.Unmarshal(scan.Bytes(), &v) == nil {
				c.messages <- v
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = w.Close()
		_ = r.Close()
		_ = rr.Close()
		_ = ww.Close()
		select {
		case <-c.done:
		case <-time.After(time.Second):
			t.Error("stdio server failed to stop")
		}
	})
	return c
}
func (c *client) send(t *testing.T, id, method string, params any) {
	t.Helper()
	v := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id != "" {
		v["id"] = id
	}
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.in.Write(append(b, '\n')); e != nil {
		t.Fatal(e)
	}
}
func (c *client) receive(t *testing.T, id string, d time.Duration) map[string]any {
	t.Helper()
	if v := c.pending[id]; v != nil {
		delete(c.pending, id)
		return v
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case v, ok := <-c.messages:
			if !ok {
				t.Fatal("stdio closed without response")
			}
			key, _ := v["id"].(string)
			if key == id {
				return v
			}
			c.pending[key] = v
		case <-timer.C:
			t.Fatalf("no response for %s within %s", id, d)
		}
	}
}
func (c *client) tool(t *testing.T, id, name string, args any) {
	t.Helper()
	c.send(t, id, "tools/call", map[string]any{"name": name, "arguments": args})
}
func structured(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	r, ok := v["result"].(map[string]any)
	if !ok {
		t.Fatalf("not a result: %+v", v)
	}
	s, ok := r["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structured data: %+v", r)
	}
	return s
}
func connectArgs(addr string) map[string]any {
	host, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	return map[string]any{"protocol": "telnet", "host": host, "port": n}
}
func TestStdioConcurrencyCancellationAndDevice(t *testing.T) {
	addr := testutil.TelnetSwitch(t)
	c := harness(t)
	c.send(t, "init", "initialize", map[string]any{"protocolVersion": "2025-11-25"})
	v := c.receive(t, "init", time.Second)
	if v["result"].(map[string]any)["serverInfo"].(map[string]any)["name"] != "keepsess" {
		t.Fatal("incorrect MCP server name", v)
	}
	if !strings.HasPrefix(v["result"].(map[string]any)["serverInfo"].(map[string]any)["version"].(string), "0.3.2+") {
		t.Fatal(v)
	}
	c.send(t, "list", "tools/list", map[string]any{})
	v = c.receive(t, "list", time.Second)
	if len(v["result"].(map[string]any)["tools"].([]any)) != 15 {
		t.Fatal("tool discovery")
	}
	c.tool(t, "connect", "sess_connect", connectArgs(addr))
	id := structured(t, c.receive(t, "connect", time.Second))["session"].(string)
	c.tool(t, "config", "sess_get_config", map[string]any{"session": id})
	conf := structured(t, c.receive(t, "config", time.Second))
	if conf["config"].(map[string]any)["read_idle_timeout_ms"] != float64(500) {
		t.Fatal(conf)
	}
	c.tool(t, "greeting", "sess_read_until", map[string]any{"session": id, "config": map[string]any{"end_pattern": "switch> "}})
	c.receive(t, "greeting", time.Second)
	c.tool(t, "read", "sess_read_until", map[string]any{"session": id, "config": map[string]any{"read_total_timeout_ms": 30000, "read_idle_timeout_ms": 0, "end_pattern": "IMPOSSIBLE"}})
	c.send(t, "ping", "ping", map[string]any{})
	c.receive(t, "ping", 500*time.Millisecond)
	c.send(t, "", "notifications/cancelled", map[string]any{"requestId": "read"})
	cancelled := structured(t, c.receive(t, "read", time.Second))
	if cancelled["reason"] != "cancelled" || cancelled["connected"] != true {
		t.Fatal(cancelled)
	}
	c.tool(t, "command", "sess_sendline_raw", map[string]any{"session": id, "data": "show version", "config": map[string]any{"end_pattern": "switch> "}})
	result := structured(t, c.receive(t, "command", 2*time.Second))
	if result["reason"] != "match" || !strings.Contains(result["output"].(string), "SwitchOS") {
		t.Fatal(result)
	}
	c.tool(t, "invalid", "sess_send_raw", map[string]any{"session": id, "data": "x", "input_id": "removed"})
	if !c.receive(t, "invalid", time.Second)["result"].(map[string]any)["isError"].(bool) {
		t.Fatal("unknown argument accepted")
	}
	c.tool(t, "configure", "sess_configure", map[string]any{"session": id, "config": map[string]any{"read_total_timeout_ms": "30*1000"}})
	if c.receive(t, "configure", time.Second)["result"].(map[string]any)["isError"] != false {
		t.Fatal("configure")
	}
	c.tool(t, "close-all", "sess_close_all", map[string]any{})
	c.receive(t, "close-all", time.Second)
	c.tool(t, "sessions", "sess_list", map[string]any{})
	if len(structured(t, c.receive(t, "sessions", time.Second))["sessions"].([]any)) != 0 {
		t.Fatal("close_all retained sessions")
	}
}
func TestBlockedStdoutExits(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	s := &Server{Manager: core.NewManager()}
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(context.Background(), strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n"), w)
	}()
	select {
	case e := <-done:
		if e == nil || time.Since(start) > 5*time.Second {
			t.Fatalf("blocked stdout: %v after %v", e, time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stdout deadlocked")
	}
}
func TestEOFDrainsFinalResponse(t *testing.T) {
	s := &Server{Manager: core.NewManager()}
	var out strings.Builder
	e := s.Serve(context.Background(), strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n"), &out)
	if e != nil || !strings.Contains(out.String(), `"result":{}`) {
		t.Fatal(e, out.String())
	}
}
func TestHalfOpenUnderTwoMinutes(t *testing.T) {
	if os.Getenv("SESSMCP_LONG_TEST") != "1" {
		t.Skip("real 110s read deadline")
	}
	addr := testutil.Listen(t, func(conn net.Conn) { _, _ = io.Copy(io.Discard, conn) })
	c := harness(t)
	c.tool(t, "connect", "sess_connect", connectArgs(addr))
	id := structured(t, c.receive(t, "connect", time.Second))["session"].(string)
	start := time.Now()
	c.tool(t, "hang", "sess_read_until", map[string]any{"session": id, "config": map[string]any{"read_total_timeout_ms": 110000, "read_idle_timeout_ms": 0}})
	c.send(t, "ping", "ping", map[string]any{})
	c.receive(t, "ping", time.Second)
	v := structured(t, c.receive(t, "hang", 118*time.Second))
	if time.Since(start) >= 120*time.Second || v["reason"] != "total_timeout" || v["connected"] != true {
		t.Fatal(v, time.Since(start))
	}
}

func TestToolSchemas(t *testing.T) {
	for _, tool := range Tools() {
		b, e := json.Marshal(tool)
		if e != nil {
			t.Fatal(e)
		}
		var decoded map[string]any
		if e = json.Unmarshal(b, &decoded); e != nil {
			t.Fatal(e)
		}
		schema := decoded["inputSchema"].(map[string]any)
		if _, ok := schema["required"].([]any); !ok {
			t.Fatalf("invalid required array: %s", b)
		}
	}
}

func TestSendJSONToWire(t *testing.T) {
	cases := []struct{ tool, data, want string }{
		{"sess_send_raw", `\r`, `\r`},
		{"sess_send_escaped", `\r`, "\r"},
		{"sess_send_escaped", `\x03`, "\x03"},
		{"sess_send_escaped", `\0`, "\x00"},
		{"sess_send_escaped", `\\r`, `\r`},
		{"sess_send_raw", "&#13;", "&#13;"},
		{"sess_sendline_raw", "", "\r\n"},
		{"sess_sendline_escaped", `a\t`, "a\t\r\n"},
	}
	wire := make(chan string, len(cases))
	addr := testutil.Listen(t, func(conn net.Conn) {
		for _, tc := range cases {
			b := make([]byte, len(tc.want))
			if _, err := io.ReadFull(conn, b); err != nil {
				return
			}
			wire <- string(b)
		}
		_, _ = io.Copy(io.Discard, conn)
	})
	c := harness(t)
	c.tool(t, "connect", "sess_connect", connectArgs(addr))
	id := structured(t, c.receive(t, "connect", time.Second))["session"].(string)
	for i, tc := range cases {
		call := fmt.Sprintf("send%d", i)
		c.tool(t, call, tc.tool, map[string]any{"session": id, "data": tc.data, "collect_output": false})
		v := structured(t, c.receive(t, call, time.Second))
		if v["input_state"] != "sent" || v["written_bytes"] != float64(len(tc.want)) {
			t.Fatal(v)
		}
		select {
		case got := <-wire:
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		case <-time.After(time.Second):
			t.Fatal("no wire bytes")
		}
	}
	c.tool(t, "close", "sess_close", map[string]any{"session": id})
	v := c.receive(t, "close", time.Second)
	if v["result"].(map[string]any)["isError"] != false || structured(t, v)["output"] != nil {
		t.Fatal(v)
	}
	c.tool(t, "after-close", "sess_read_all", map[string]any{"session": id})
	if c.receive(t, "after-close", time.Second)["result"].(map[string]any)["isError"] != true {
		t.Fatal("read after close")
	}
}

func TestExecUsesIndependentWaitThroughMCP(t *testing.T) {
	addr, key := testutil.SSH(t)
	args := connectArgs(addr)
	args["protocol"] = "ssh"
	args["username"] = "test"
	args["password"] = "test"
	args["host_key"] = key
	args["mode"] = "exec"
	args["config"] = map[string]any{"read_total_timeout_ms": 1, "read_idle_timeout_ms": 1, "end_pattern": "first", "exec_total_timeout_ms": "1*1000"}
	c := harness(t)
	c.tool(t, "exec-connect", "sess_connect", args)
	id := structured(t, c.receive(t, "exec-connect", time.Second))["session"].(string)
	c.tool(t, "exec-full", "sess_exec", map[string]any{"session": id, "command": "slow"})
	v := structured(t, c.receive(t, "exec-full", 2*time.Second))
	if v["reason"] != "exec_finish" || v["exit_code"] != float64(0) || !strings.Contains(v["output"].(string), "second") {
		t.Fatal(v)
	}
	c.tool(t, "exec-partial", "sess_exec", map[string]any{"session": id, "command": "slow", "config": map[string]any{"exec_total_timeout_ms": 30}})
	v = structured(t, c.receive(t, "exec-partial", time.Second))
	if v["reason"] != "total_timeout" || v["running"] != true || v["connected"] != true || v["input_state"] != "sent" {
		t.Fatal(v)
	}
	c.tool(t, "exec-config", "sess_get_config", map[string]any{"session": id})
	conf := structured(t, c.receive(t, "exec-config", time.Second))["config"].(map[string]any)
	if conf["exec_total_timeout_ms"] != float64(1000) || conf["read_total_timeout_ms"] != float64(1) || conf["end_pattern"] != "first" {
		t.Fatal(conf)
	}
	c.tool(t, "exec-drain", "sess_read_until", map[string]any{"session": id, "config": map[string]any{"read_total_timeout_ms": 1000, "read_idle_timeout_ms": 0, "end_pattern": ""}})
	v = structured(t, c.receive(t, "exec-drain", 2*time.Second))
	if v["reason"] != "exec_finish" || !strings.Contains(v["output"].(string), "second") {
		t.Fatal(v)
	}
	c.tool(t, "exec-no-collect", "sess_exec", map[string]any{"session": id, "command": "quiet", "collect_output": false, "config": map[string]any{"exec_total_timeout_ms": 1}})
	v = structured(t, c.receive(t, "exec-no-collect", time.Second))
	if v["input_state"] != "sent" || v["reason"] != nil || v["running"] != true {
		t.Fatal(v)
	}
	c.tool(t, "exec-quiet-drain", "sess_read_until", map[string]any{"session": id, "config": map[string]any{"read_total_timeout_ms": 2000, "read_idle_timeout_ms": 0, "end_pattern": ""}})
	v = structured(t, c.receive(t, "exec-quiet-drain", 3*time.Second))
	if v["reason"] != "exec_finish" || v["output"] != "quiet done\n" {
		t.Fatal(v)
	}
}
