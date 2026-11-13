package mcp

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
)

func TestExecTopLevelWaitOptions(t *testing.T) {
	c := harness(t)
	sequence := 0
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		sequence++
		id := "options" + strconv.Itoa(sequence)
		c.tool(t, id, name, args)
		return structured(t, c.receive(t, id, 3*time.Second))
	}
	addr, _ := testutil.SSH(t)
	args := connectArgs(addr)
	args["protocol"], args["username"], args["password"] = "ssh", "test", "test"
	args["exec_timeout_ms"] = 120000
	args["read_timeout_ms"] = 30000
	args["encode_errors"] = "error"
	args["decode_errors"] = "to_escape_bytes"
	id := call("connect", args)["session"]
	confResult := call("configure", map[string]any{
		"session_id": id, "read_timeout_ms": 30000,
		"config": map[string]any{"read_timeout_ms": 1},
	})
	if confResult["config"].(map[string]any)["read_total_timeout_ms"] != float64(30000) {
		t.Fatal(confResult)
	}
	if v := call("configure", map[string]any{"session_id": id, "read_timeout_ms": 20, "read_total_timeout_ms": 30}); v["error"] == nil {
		t.Fatal(v)
	}
	if v := call("task_list", map[string]any{"session_id": id}); v["error"] != nil {
		t.Fatal(v)
	}
	for _, tc := range []map[string]any{
		{"term_on_timeout": nil}, {"term_on_timeout": "false"},
		{"exec_total_timeout_ms": nil}, {"exec_total_timeout_ms": 0},
		{"exec_total_timeout_ms": 300001}, {"exec_total_timeout_ms": "bad"},
		{"timeout_ms": nil}, {"timeout_ms": 0}, {"timeout_ms": 300001},
		{"timeout_ms": "bad"}, {"timeout_ms": true}, {"timeout_ms": "1/2"},
		{"timeout_ms": 10, "exec_total_timeout_ms": 20},
		{"timeout_ms": 10, "exec_total_timeout_ms": nil},
	} {
		tc["command"] = "hang"
		if v := call("exec", tc); v["error"] == nil {
			t.Fatal(v)
		}
	}
	if v := call("task_list", map[string]any{}); len(v["tasks"].([]any)) != 0 {
		t.Fatalf("invalid options launched tasks: %v", v)
	}
	// Top-level false must override nested true; timeout must override nested 1ms.
	v := call("exec", map[string]any{
		"cmd": "hang", "timeout_ms": "2*10", "term_on_timeout": false,
		"config": map[string]any{"exec_total_timeout_ms": 1, "term_on_timeout": true},
	})
	if v["reason"] != "total_timeout" || v["signal"] != nil || v["session"] != id || v["handle"] == nil {
		t.Fatal(v)
	}
	handle := v["handle"]
	v = call("task_wait", map[string]any{
		"task_id": handle, "execution_timeout_ms": 10, "term_on_timeout": false,
		"config": map[string]any{"term_on_timeout": true},
	})
	if v["reason"] != "total_timeout" || v["signal"] != nil {
		t.Fatal(v)
	}
	// Invalid wait aliases must not read output or signal the existing task.
	for _, options := range []map[string]any{
		{"timeout_ms": 10, "exec_total_timeout_ms": 11},
		{"timeout_ms": nil}, {"timeout_ms": 300001},
	} {
		options["handle"] = handle
		if v := call("task_wait", options); v["error"] == nil {
			t.Fatal(v)
		}
	}
	if v := call("task_status", map[string]any{"handle": handle}); v["signal"] != nil {
		t.Fatalf("invalid wait signalled task: %v", v)
	}
	// A longer top-level wait must beat a short nested timeout on both tools.
	v = call("exec", map[string]any{
		"command": "slow", "timeout_ms": "1*1000", "exec_total_timeout_ms": 1000,
		"config": map[string]any{"exec_total_timeout_ms": 1},
	})
	if v["reason"] != "exec_finish" || v["exit_code"] != float64(0) {
		t.Fatal(v)
	}
	v = call("exec", map[string]any{"command": "slow", "collect_output": false})
	v = call("task_wait", map[string]any{
		"handle": v["handle"], "timeout_ms": 1000, "exec_total_timeout_ms": "2*500",
		"config": map[string]any{"exec_total_timeout_ms": 1},
	})
	if v["reason"] != "exec_finish" || v["exit_code"] != float64(0) {
		t.Fatal(v)
	}
	conf := call("get_config", map[string]any{"session": id})["config"].(map[string]any)
	if conf["exec_total_timeout_ms"] != float64(120000) || conf["term_on_timeout"] != true {
		t.Fatal(conf)
	}
	// Explicit top-level true still requests TERM on the configured timeout.
	v = call("task_wait", map[string]any{
		"handle": handle, "timeout_ms": 10, "term_on_timeout": true,
		"config": map[string]any{"term_on_timeout": false},
	})
	if v["reason"] != "total_timeout" || v["signal"] == nil {
		t.Fatal(v)
	}
	v = call("task_status", map[string]any{"handle": handle, "term_on_timeout": false})
	if !strings.Contains(v["error"].(string), "unknown argument") {
		t.Fatal(v)
	}
	for _, tool := range []string{"exec", "task_wait"} {
		props := definitions[tool]["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if props["timeout_ms"] == nil || props["exec_total_timeout_ms"] == nil || props["term_on_timeout"] == nil {
			t.Fatal(tool, props)
		}
	}
}

func TestTimeoutAliasOnlyDiscoverableOnWaitTools(t *testing.T) {
	for _, value := range Tools() {
		def := value.(map[string]any)
		name := def["name"].(string)
		props := def["inputSchema"].(map[string]any)["properties"].(map[string]any)
		expected := name == "exec" || name == "task_wait"
		if (props["timeout_ms"] != nil) != expected {
			t.Fatalf("unexpected timeout_ms discovery on %s", name)
		}
	}
	s := &Server{}
	for _, name := range []string{"connect", "configure", "read_until", "send_text"} {
		// Validation runs before any session operation or connection attempt.
		args := map[string]any{"timeout_ms": 10, "session": "s1"}
		if name == "connect" {
			args = map[string]any{"timeout_ms": 10}
		}
		v := directCall(t, s, name, args)
		if !strings.Contains(v["error"].(string), `unknown argument "timeout_ms"`) {
			t.Fatal(name, v)
		}
	}
	for _, name := range []string{"exec", "task_wait"} {
		help, err := builtinHelp(helpRequest{Topic: name, Parameter: "timeout_ms"})
		if err != nil {
			t.Fatal(name, err)
		}
		text := fmt.Sprint(help)
		for _, fragment := range []string{"300000ms", "RPC budget", "TERM", "same value", "inherit"} {
			if !strings.Contains(text, fragment) {
				t.Fatalf("%s help missing %q: %v", name, fragment, help)
			}
		}
	}
}
