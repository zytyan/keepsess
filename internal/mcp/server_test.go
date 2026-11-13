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
	"golang.org/x/crypto/ssh"
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
		scan.Buffer(make([]byte, 4096), 8<<20)
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
	if len(v["result"].(map[string]any)["tools"].([]any)) != len(Tools()) {
		t.Fatal("tool discovery")
	}
	c.tool(t, "connect", "connect", connectArgs(addr))
	id := structured(t, c.receive(t, "connect", time.Second))["session"].(string)
	c.tool(t, "config", "get_config", map[string]any{"session": id})
	conf := structured(t, c.receive(t, "config", time.Second))
	if conf["config"].(map[string]any)["read_idle_timeout_ms"] != float64(500) {
		t.Fatal(conf)
	}
	c.tool(t, "greeting", "read_until", map[string]any{"session": id, "config": map[string]any{"end_pattern": "switch> "}})
	c.receive(t, "greeting", time.Second)
	c.tool(t, "read", "read_until", map[string]any{"session": id, "config": map[string]any{"read_total_timeout_ms": 30000, "read_idle_timeout_ms": 0, "end_pattern": "IMPOSSIBLE"}})
	c.send(t, "ping", "ping", map[string]any{})
	c.receive(t, "ping", 500*time.Millisecond)
	c.send(t, "", "notifications/cancelled", map[string]any{"requestId": "read"})
	cancelled := structured(t, c.receive(t, "read", time.Second))
	if cancelled["reason"] != "cancelled" || cancelled["connected"] != true {
		t.Fatal(cancelled)
	}
	c.tool(t, "command", "sendline_text", map[string]any{"session": id, "data": "show version", "config": map[string]any{"end_pattern": "switch> "}})
	result := structured(t, c.receive(t, "command", 2*time.Second))
	if result["reason"] != "match" || !strings.Contains(result["output"].(string), "SwitchOS") {
		t.Fatal(result)
	}
	c.tool(t, "invalid", "send_text", map[string]any{"session": id, "data": "x", "input_id": "removed"})
	if !c.receive(t, "invalid", time.Second)["result"].(map[string]any)["isError"].(bool) {
		t.Fatal("unknown argument accepted")
	}
	c.tool(t, "configure", "configure", map[string]any{"session": id, "config": map[string]any{"read_total_timeout_ms": "30*1000"}})
	if c.receive(t, "configure", time.Second)["result"].(map[string]any)["isError"] != false {
		t.Fatal("configure")
	}
	c.tool(t, "close-all", "close_all", map[string]any{})
	c.receive(t, "close-all", time.Second)
	c.tool(t, "sessions", "list", map[string]any{})
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
	c.tool(t, "connect", "connect", connectArgs(addr))
	id := structured(t, c.receive(t, "connect", time.Second))["session"].(string)
	start := time.Now()
	c.tool(t, "hang", "read_until", map[string]any{"session": id, "config": map[string]any{"read_total_timeout_ms": 110000, "read_idle_timeout_ms": 0}})
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

func TestStdioTaskLifecycleAndConcurrentWait(t *testing.T) {
	addr, key := testutil.SSH(t)
	c := harness(t)
	args := connectArgs(addr)
	args["protocol"], args["mode"], args["username"], args["password"], args["host_key"] = "ssh", "exec", "test", "test", key
	c.tool(t, "connect", "connect", args)
	session := structured(t, c.receive(t, "connect", time.Second))["session"]
	c.tool(t, "async", "exec", map[string]any{"session": session, "command": "hang", "collect_output": false})
	a := structured(t, c.receive(t, "async", time.Second))
	handle, ok := a["handle"].(string)
	if !ok || handle != "task1" || a["comment"] != nil {
		t.Fatal(a)
	}
	c.tool(t, "wait", "task_wait", map[string]any{"handle": handle, "config": map[string]any{"term_on_timeout": false}})
	c.tool(t, "status", "task_status", map[string]any{"handle": handle})
	v := structured(t, c.receive(t, "status", time.Second))
	if v["state"] != "running" || v["output"] != nil {
		t.Fatal(v)
	}
	c.tool(t, "sync", "exec", map[string]any{"session": session, "command": "both"})
	v = structured(t, c.receive(t, "sync", time.Second))
	if v["reason"] != "exec_finish" || v["handle"] != nil {
		t.Fatal(v)
	}
	c.tool(t, "signal", "task_signal", map[string]any{"handle": handle, "signal": "TERM"})
	v = structured(t, c.receive(t, "signal", time.Second))
	if v["signal"].(map[string]any)["request_state"] != "sent" {
		t.Fatal(v)
	}
	c.send(t, "", "notifications/cancelled", map[string]any{"requestId": "wait"})
	v = structured(t, c.receive(t, "wait", time.Second))
	if v["reason"] != "cancelled" || v["handle"] != handle || v["comment"] == nil {
		t.Fatal(v)
	}
	for i, args := range []map[string]any{
		{"session": session},
		{"session": session, "handle": handle},
		{"session": "", "handle": handle},
		{},
	} {
		id := fmt.Sprintf("invalid-read-%d", i)
		c.tool(t, id, "read_available", args)
		if c.receive(t, id, time.Second)["result"].(map[string]any)["isError"] != true {
			t.Fatal("ambiguous/missing target accepted", args)
		}
	}
	c.tool(t, "live-release", "task_release", map[string]any{"handle": handle})
	if c.receive(t, "live-release", time.Second)["result"].(map[string]any)["isError"] != true {
		t.Fatal("running task released")
	}
	c.tool(t, "list", "task_list", map[string]any{"session": session})
	list := structured(t, c.receive(t, "list", time.Second))["tasks"].([]any)
	if len(list) != 2 {
		t.Fatal(list)
	}
	var finished string
	for _, item := range list {
		task := item.(map[string]any)
		if task["state"] == "finished" {
			finished = task["handle"].(string)
		}
	}
	c.tool(t, "history", "read_history", map[string]any{"handle": finished})
	v = structured(t, c.receive(t, "history", time.Second))
	if !strings.Contains(v["output"].(string), "stdout") || !strings.Contains(v["output"].(string), "stderr") {
		t.Fatal(v)
	}
	c.tool(t, "release", "task_release", map[string]any{"handle": finished})
	if v = structured(t, c.receive(t, "release", time.Second)); v["reason"] != "released" {
		t.Fatal(v)
	}
	c.tool(t, "close", "close", map[string]any{"session": session})
	c.receive(t, "close", time.Second)
	c.tool(t, "gone", "task_status", map[string]any{"handle": handle})
	if c.receive(t, "gone", time.Second)["result"].(map[string]any)["isError"] != true {
		t.Fatal("handle survived close")
	}
}

func TestSendJSONToWire(t *testing.T) {
	cases := []struct{ tool, data, want string }{
		{"send_text", `\r`, `\r`},
		{"send_escaped", `\r`, "\r"},
		{"send_escaped", `\x03`, "\x03"},
		{"send_escaped", `\0`, "\x00"},
		{"send_escaped", `\\r`, `\r`},
		{"send_text", "&#13;", "&#13;"},
		{"sendline_text", "", "\r\n"},
		{"sendline_escaped", `a\t`, "a\t\r\n"},
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
	c.tool(t, "connect", "connect", connectArgs(addr))
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
	c.tool(t, "close", "close", map[string]any{"session": id})
	v := c.receive(t, "close", time.Second)
	if v["result"].(map[string]any)["isError"] != false || structured(t, v)["output"] != nil {
		t.Fatal(v)
	}
	c.tool(t, "after-close", "read_history", map[string]any{"session": id})
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
	c.tool(t, "exec-connect", "connect", args)
	id := structured(t, c.receive(t, "exec-connect", time.Second))["session"].(string)
	c.tool(t, "exec-full", "exec", map[string]any{"session": id, "command": "slow"})
	v := structured(t, c.receive(t, "exec-full", 2*time.Second))
	if v["reason"] != "exec_finish" || v["exit_code"] != float64(0) || !strings.Contains(v["output"].(string), "second") {
		t.Fatal(v)
	}
	c.tool(t, "exec-partial", "exec", map[string]any{"session": id, "command": "slow", "config": map[string]any{"exec_total_timeout_ms": 30}})
	v = structured(t, c.receive(t, "exec-partial", time.Second))
	if v["reason"] != "total_timeout" || v["running"] != true || v["connected"] != true || v["input_state"] != "sent" {
		t.Fatal(v)
	}
	c.tool(t, "exec-config", "get_config", map[string]any{"session": id})
	conf := structured(t, c.receive(t, "exec-config", time.Second))["config"].(map[string]any)
	if conf["exec_total_timeout_ms"] != float64(1000) || conf["read_total_timeout_ms"] != float64(1) || conf["end_pattern"] != "first" {
		t.Fatal(conf)
	}
	c.tool(t, "exec-drain", "read_until", map[string]any{"handle": v["handle"], "config": map[string]any{"read_total_timeout_ms": 1000, "read_idle_timeout_ms": 0, "end_pattern": ""}})
	v = structured(t, c.receive(t, "exec-drain", 2*time.Second))
	if v["reason"] != "exec_finish" || !strings.Contains(v["output"].(string), "second") {
		t.Fatal(v)
	}
	c.tool(t, "exec-no-collect", "exec", map[string]any{"session": id, "command": "quiet", "collect_output": false, "config": map[string]any{"exec_total_timeout_ms": 1}})
	v = structured(t, c.receive(t, "exec-no-collect", time.Second))
	if v["input_state"] != "sent" || v["reason"] != nil || v["running"] != true {
		t.Fatal(v)
	}
	c.tool(t, "exec-quiet-drain", "read_until", map[string]any{"handle": v["handle"], "config": map[string]any{"read_total_timeout_ms": 2000, "read_idle_timeout_ms": 0, "end_pattern": ""}})
	v = structured(t, c.receive(t, "exec-quiet-drain", 3*time.Second))
	if v["reason"] != "exec_finish" || v["output"] != "quiet done\n" {
		t.Fatal(v)
	}
}

func TestExecRejectsInteractiveStreamsThroughMCP(t *testing.T) {
	for _, protocol := range []string{"ssh", "telnet"} {
		t.Run(protocol, func(t *testing.T) {
			var addr, key string
			if protocol == "ssh" {
				addr, key = testutil.SSHWithShell(t, func(rw io.ReadWriter) { _, _ = io.Copy(io.Discard, rw) })
			} else {
				addr = testutil.Listen(t, func(conn net.Conn) { _, _ = io.Copy(io.Discard, conn) })
			}
			args := connectArgs(addr)
			args["protocol"] = protocol
			args["mode"] = "stream"
			if protocol == "ssh" {
				args["username"] = "test"
				args["password"] = "test"
				args["host_key"] = key
			}
			c := harness(t)
			c.tool(t, "connect-stream", "connect", args)
			id := structured(t, c.receive(t, "connect-stream", time.Second))["session"].(string)
			c.tool(t, "invalid-exec", "exec", map[string]any{"session": id, "command": "must-not-execute"})
			v := c.receive(t, "invalid-exec", time.Second)
			if v["result"].(map[string]any)["isError"] != true || !strings.Contains(structured(t, v)["error"].(string), "SSH mode=exec") {
				t.Fatal(v)
			}
			c.tool(t, "valid-text", "send_text", map[string]any{"session": id, "data": "test", "collect_output": false})
			if v := structured(t, c.receive(t, "valid-text", time.Second)); v["input_state"] != "sent" || v["connected"] != true {
				t.Fatal("mode rejection damaged stream", v)
			}
		})
	}
}

func TestToolNamesAndRemovedAliases(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range Tools() {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{"send_text", "sendline_text", "read_history"} {
		if !names[name] {
			t.Fatal("missing renamed tool", name)
		}
	}
	server := &Server{Manager: core.NewManager()}
	defer server.Manager.CloseAll()
	for _, name := range []string{"send_raw", "sendline_raw", "read_all"} {
		if names[name] {
			t.Fatal("legacy alias still exposed", name)
		}
		params, _ := json.Marshal(map[string]any{"name": name, "arguments": map[string]any{}})
		result := server.call(context.Background(), params).(map[string]any)
		if result["isError"] != true {
			t.Fatal("removed tool still callable", name)
		}
	}
}

func TestReadOnlyConfigSchemas(t *testing.T) {
	for _, override := range []bool{false, true} {
		s := configSchema(override).(map[string]any)
		p := s["properties"].(map[string]any)
		v, exists := p["read_only"]
		if exists == override {
			t.Fatalf("read_only presence for override=%v: %v", override, exists)
		}
		if exists {
			field := v.(map[string]any)
			if field["type"] != "boolean" || field["default"] != false {
				t.Fatal(field)
			}
		}
	}
}

func TestReadOnlyMCPObservation(t *testing.T) {
	c := harness(t)
	args := connectArgs(testutil.TelnetSwitch(t))
	args["config"] = map[string]any{"read_only": true}
	c.tool(t, "ro-connect", "connect", args)
	id := structured(t, c.receive(t, "ro-connect", time.Second))["session"].(string)
	c.tool(t, "ro-send", "sendline_text", map[string]any{"session": id, "data": "show version"})
	v := c.receive(t, "ro-send", time.Second)
	out := structured(t, v)
	if v["result"].(map[string]any)["isError"] != true || out["input_state"] != "not_sent" || !strings.HasPrefix(out["error"].(string), "read_only:") {
		t.Fatal(v)
	}
	c.tool(t, "ro-read", "read_until", map[string]any{"session": id, "config": map[string]any{"end_pattern": "switch> "}})
	if v = c.receive(t, "ro-read", time.Second); v["result"].(map[string]any)["isError"] != false || !strings.Contains(structured(t, v)["output"].(string), "switch> ") {
		t.Fatal(v)
	}
	c.tool(t, "ro-disable", "configure", map[string]any{"session": id, "config": map[string]any{"read_only": false}})
	if v = c.receive(t, "ro-disable", time.Second); v["result"].(map[string]any)["isError"] != false {
		t.Fatal(v)
	}
	c.tool(t, "ro-send-ok", "sendline_text", map[string]any{"session": id, "data": "show version", "config": map[string]any{"end_pattern": "switch> "}})
	if v = c.receive(t, "ro-send-ok", time.Second); structured(t, v)["input_state"] != "sent" || v["result"].(map[string]any)["isError"] != false {
		t.Fatal(v)
	}
}

func TestMCPReconnectPreservesHandleAndReadOnly(t *testing.T) {
	c := harness(t)
	args := connectArgs(testutil.TelnetSwitch(t))
	args["config"] = map[string]any{"read_only": true}
	c.tool(t, "rc-connect", "connect", args)
	id := structured(t, c.receive(t, "rc-connect", time.Second))["session"].(string)
	c.tool(t, "rc-read-old", "read_until", map[string]any{"session": id, "config": map[string]any{"end_pattern": "switch> "}})
	c.receive(t, "rc-read-old", time.Second)
	c.tool(t, "rc-reconnect", "reconnect", map[string]any{"session": id})
	msg := c.receive(t, "rc-reconnect", time.Second)
	v := structured(t, msg)
	if msg["result"].(map[string]any)["isError"] != false || v["session"] != id || v["connected"] != true || v["output"] != nil {
		t.Fatal(msg)
	}
	c.tool(t, "rc-config", "get_config", map[string]any{"session": id})
	v = structured(t, c.receive(t, "rc-config", time.Second))
	if v["config"].(map[string]any)["read_only"] != true || v["password"] != nil {
		t.Fatal(v)
	}
	c.tool(t, "rc-read-new", "read_until", map[string]any{"session": id, "config": map[string]any{"end_pattern": "switch> "}})
	v = structured(t, c.receive(t, "rc-read-new", time.Second))
	if !strings.Contains(v["output"].(string), "Welcome") {
		t.Fatal(v)
	}
	c.tool(t, "rc-close", "close", map[string]any{"session": id})
	c.receive(t, "rc-close", time.Second)
	c.tool(t, "rc-closed", "reconnect", map[string]any{"session": id})
	if msg = c.receive(t, "rc-closed", time.Second); msg["result"].(map[string]any)["isError"] != true {
		t.Fatal(msg)
	}
}

func TestMCPHealthMetadataAndBudgets(t *testing.T) {
	addr, key := testutil.SSH(t)
	c := harness(t)
	args := connectArgs(addr)
	args["protocol"] = "ssh"
	args["username"] = "test"
	args["password"] = "test"
	c.tool(t, "health-connect", "connect", args)
	v := structured(t, c.receive(t, "health-connect", time.Second))
	id := v["session"].(string)
	peer, ok := v["peer"].(map[string]any)
	if !ok || peer["host_key"] != key || peer["server"] == "" {
		t.Fatal(v)
	}
	c.tool(t, "health-config", "get_config", map[string]any{"session": id})
	v = structured(t, c.receive(t, "health-config", time.Second))
	conf := v["config"].(map[string]any)
	if conf["session_idle_timeout_ms"] != float64(7200000) || conf["exec_total_timeout_ms"] != float64(120000) || conf["max_output_bytes"] != float64(65536) || conf["ssh_keepalive_ms"] != float64(30000) {
		t.Fatal(conf)
	}
	c.tool(t, "health-close", "close", map[string]any{"session": id})
	c.receive(t, "health-close", time.Second)
	c.tool(t, "health-closed-read", "read_available", map[string]any{"session": id})
	msg := c.receive(t, "health-closed-read", time.Second)
	v = structured(t, msg)
	if msg["result"].(map[string]any)["isError"] != true || v["disconnect_reason"] != "explicit_close" || v["next_action"] != "connect" || v["session"] != id {
		t.Fatal(msg)
	}
	for _, key := range []string{"ssh_keepalive_ms", "ssh_keepalive_timeout_ms"} {
		properties := configSchema(true).(map[string]any)["properties"].(map[string]any)
		if _, ok := properties[key]; ok {
			t.Fatal("connection config in operation schema", key)
		}
	}
}

func TestMCPMaximumOutputPage(t *testing.T) {
	addr := testutil.Listen(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, strings.Repeat("x", 600<<10))
		_, _ = io.Copy(io.Discard, conn)
	})
	c := harness(t)
	c.tool(t, "page-connect", "connect", connectArgs(addr))
	id := structured(t, c.receive(t, "page-connect", time.Second))["session"].(string)
	c.tool(t, "page-max", "read_until", map[string]any{"session": id, "config": map[string]any{"max_output_bytes": 512 << 10, "read_idle_timeout_ms": 0}})
	v := structured(t, c.receive(t, "page-max", 3*time.Second))
	if len(v["output"].(string)) != 512<<10 || v["reason"] != "output_limit" {
		t.Fatal("maximum page failed")
	}
	c.tool(t, "page-default", "read_until", map[string]any{"session": id, "config": map[string]any{"read_idle_timeout_ms": 0}})
	v = structured(t, c.receive(t, "page-default", 3*time.Second))
	if len(v["output"].(string)) != 64<<10 {
		t.Fatal("default page failed")
	}
}

func TestMCPInteractiveTaskInput(t *testing.T) {
	addr, _ := testutil.SSHWithExec(t, func(_ string, ch ssh.Channel, _ <-chan *ssh.Request) {
		_, _ = io.Copy(ch, ch)
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
	})
	c := harness(t)
	args := connectArgs(addr)
	args["protocol"] = "ssh"
	args["username"] = "test"
	args["password"] = "test"
	c.tool(t, "i-connect", "connect", args)
	id := structured(t, c.receive(t, "i-connect", time.Second))["session"].(string)
	c.tool(t, "i-start", "exec", map[string]any{"session": id, "command": "echo", "interactive": true, "pty": true, "collect_output": false})
	v := structured(t, c.receive(t, "i-start", time.Second))
	handle := v["handle"].(string)
	if v["stdin_open"] != true || v["pty"] != true || v["interactive"] != true {
		t.Fatal(v)
	}
	c.tool(t, "i-send", "sendline_escaped", map[string]any{"handle": handle, "data": `hello\x21`, "config": map[string]any{"line_end": `\n`, "read_idle_timeout_ms": 10}})
	v = structured(t, c.receive(t, "i-send", time.Second))
	if v["input_state"] != "sent" || v["output"] != "hello!\n" {
		t.Fatal(v)
	}
	c.tool(t, "i-eof", "task_close_stdin", map[string]any{"handle": handle})
	v = structured(t, c.receive(t, "i-eof", time.Second))
	if v["input_state"] != "sent" || v["stdin_open"] != false {
		t.Fatal(v)
	}
	c.tool(t, "i-wait", "task_wait", map[string]any{"handle": handle})
	v = structured(t, c.receive(t, "i-wait", time.Second))
	if v["reason"] != "exec_finish" || v["exit_code"] != float64(0) {
		t.Fatal(v)
	}
	c.tool(t, "i-bad-pty", "exec", map[string]any{"session": id, "command": "echo", "pty": true})
	if msg := c.receive(t, "i-bad-pty", time.Second); msg["result"].(map[string]any)["isError"] != true {
		t.Fatal(msg)
	}
	for _, tool := range []string{"send_text", "send_escaped", "sendline_text", "sendline_escaped"} {
		schema := definitions[tool]["inputSchema"].(map[string]any)
		if len(schema["oneOf"].([]any)) != 2 || len(schema["required"].([]string)) != 1 || schema["required"].([]string)[0] != "data" {
			t.Fatal(tool, schema)
		}
	}
}
