package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
)

func TestStdioExecOptionalSession(t *testing.T) {
	c := harness(t)
	call := func(id, name string, args map[string]any) map[string]any {
		t.Helper()
		c.tool(t, id, name, args)
		return structured(t, c.receive(t, id, 3*time.Second))
	}
	if v := call("none", "sess_exec", map[string]any{"command": "both"}); !strings.Contains(v["error"].(string), "sess_connect") {
		t.Fatal(v)
	}
	addr, _ := testutil.SSH(t)
	args := connectArgs(addr)
	args["protocol"], args["username"], args["password"] = "ssh", "test", "test"
	id := call("connect", "sess_connect", args)["session"].(string)
	v := call("exec", "sess_exec", map[string]any{"command": "both"})
	if v["session"] != id || v["exit_code"] != float64(0) {
		t.Fatal(v)
	}
	for _, invalid := range []any{"", nil, 123} {
		v = call("invalid", "sess_exec", map[string]any{"session": invalid, "command": "both"})
		if !strings.Contains(v["error"].(string), "non-empty string") {
			t.Fatal(v)
		}
	}
	v = call("missing-target", "sess_exec", map[string]any{"session": "missing", "command": "both"})
	if v["session"] != "missing" || v["error"] == nil {
		t.Fatal(v)
	}
	call("connect2", "sess_connect", args)
	v = call("ambiguous", "sess_exec", map[string]any{"command": "both"})
	if !strings.Contains(v["error"].(string), "multiple retained sessions") {
		t.Fatal(v)
	}
	v = call("tasks", "sess_task_list", map[string]any{})
	if len(v["tasks"].([]any)) != 1 {
		t.Fatal(v)
	}
	v = call("handle-required", "sess_task_status", map[string]any{})
	if !strings.Contains(v["error"].(string), "handle") {
		t.Fatal(v)
	}
	schema := definitions["sess_exec"]["inputSchema"].(map[string]any)
	required := schema["required"].([]string)
	if len(required) != 1 || required[0] != "command" {
		t.Fatal(schema)
	}
}
