package mcp

import (
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
	id := call("connect", args)["session"]
	for _, tc := range []map[string]any{
		{"term_on_timeout": nil}, {"term_on_timeout": "false"},
		{"exec_total_timeout_ms": nil}, {"exec_total_timeout_ms": 0},
		{"exec_total_timeout_ms": 300001}, {"exec_total_timeout_ms": "bad"},
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
		"command": "hang", "exec_total_timeout_ms": "2*10", "term_on_timeout": false,
		"config": map[string]any{"exec_total_timeout_ms": 1, "term_on_timeout": true},
	})
	if v["reason"] != "total_timeout" || v["signal"] != nil || v["session"] != id || v["handle"] == nil {
		t.Fatal(v)
	}
	handle := v["handle"]
	v = call("task_wait", map[string]any{
		"handle": handle, "exec_total_timeout_ms": 10, "term_on_timeout": false,
		"config": map[string]any{"term_on_timeout": true},
	})
	if v["reason"] != "total_timeout" || v["signal"] != nil {
		t.Fatal(v)
	}
	// A longer top-level wait must beat a short nested timeout on both tools.
	v = call("exec", map[string]any{
		"command": "slow", "exec_total_timeout_ms": "1*1000",
		"config": map[string]any{"exec_total_timeout_ms": 1},
	})
	if v["reason"] != "exec_finish" || v["exit_code"] != float64(0) {
		t.Fatal(v)
	}
	v = call("exec", map[string]any{"command": "slow", "collect_output": false})
	v = call("task_wait", map[string]any{
		"handle": v["handle"], "exec_total_timeout_ms": 1000,
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
		"handle": handle, "exec_total_timeout_ms": 10, "term_on_timeout": true,
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
		if props["exec_total_timeout_ms"] == nil || props["term_on_timeout"] == nil {
			t.Fatal(tool, props)
		}
	}
}
