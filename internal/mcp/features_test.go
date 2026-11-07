package mcp

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
)

func TestNamedSessionsTasksInstanceDiagnosticsAndViews(t *testing.T) {
	c := harness(t)
	sequence := 0
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		sequence++
		id := strconv.Itoa(sequence)
		c.tool(t, id, tool, args)
		return structured(t, c.receive(t, id, 3*time.Second))
	}
	addr, _ := testutil.SSH(t)
	args := connectArgs(addr)
	args["protocol"], args["username"], args["password"], args["name"] = "ssh", "test", "test", "prod"
	v := call("sess_connect", args)
	id := v["session"]
	instance := v["instance_id"]
	if len(instance.(string)) != 8 || v["name"] != "prod" {
		t.Fatal(v)
	}
	v = call("sess_rename", map[string]any{"session": "prod", "name": "server"})
	if v["session"] != id || v["name"] != "server" {
		t.Fatal(v)
	}
	v = call("sess_exec", map[string]any{"session": "server", "command": "hang", "name": "job", "collect_output": false, "login": false})
	handle := v["handle"]
	if v["name"] != "job" || v["input_state"] != "sent" {
		t.Fatal(v)
	}
	v = call("sess_exec", map[string]any{"session": id, "command": "must-not-run", "name": "job", "collect_output": false})
	if v["input_state"] != "not_sent" || !strings.Contains(v["error"].(string), "duplicate") {
		t.Fatal(v)
	}
	v = call("sess_task_rename", map[string]any{"handle": "job", "name": "compile"})
	if v["handle"] != handle || v["name"] != "compile" {
		t.Fatal(v)
	}
	v = call("sess_task_status", map[string]any{"handle": "compile"})
	if v["handle"] != handle {
		t.Fatal(v)
	}
	v = call("sess_task_wait", map[string]any{"handle": "compile", "exec_total_timeout_ms": 10, "term_on_timeout": false})
	if v["timeout_source"] != "exec_wait" || v["wait_elapsed_ms"] == nil || v["started_at"] == nil || v["task_elapsed_ms"] == nil {
		t.Fatal(v)
	}
	v = call("sess_read_available", map[string]any{"handle": handle, "session": id})
	if !strings.Contains(v["error"].(string), "remove session") {
		t.Fatal(v)
	}
	v = call("sess_get_config", map[string]any{"session": "gone"})
	if v["instance_id"] != instance || !strings.Contains(v["error"].(string), "instance changed") {
		t.Fatal(v)
	}
	v = call("sess_configure", map[string]any{"session": "server", "strip_ansi": true, "include_pattern": "stdout"})
	if v["config"].(map[string]any)["strip_ansi"] != true {
		t.Fatal(v)
	}
	v = call("sess_exec", map[string]any{"session": "server", "command": "both"})
	if v["output"] != "stdout\n" || v["filtered_lines"] != float64(1) {
		t.Fatal(v)
	}
	call("sess_close", map[string]any{"session": "server"})
}

func TestHopErrorsIdentifyEndpointAndOrigin(t *testing.T) {
	c := harness(t)
	target, _ := testutil.SSH(t)
	addr, key, _ := testutil.SSHJump(t, target, false)
	jump := connectArgs(addr)
	delete(jump, "protocol")
	jump["username"], jump["password"], jump["host_key"] = "jump-user", "jump-secret", key
	args := connectArgs(target)
	args["protocol"], args["username"], args["password"], args["host_key"] = "ssh", "test", "test", "SHA256:bad"
	args["ssh_jumps"] = []any{jump}
	c.tool(t, "route", "sess_connect", args)
	v := structured(t, c.receive(t, "route", 3*time.Second))
	if v["hop_index"] != float64(2) || v["host"] != args["host"] || v["from_host"] != jump["host"] || v["stage"] != "handshake" || v["target"] != true || !strings.Contains(v["error"].(string), "ssh_jumps[0]") {
		t.Fatal(v)
	}
}
