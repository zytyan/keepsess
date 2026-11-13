package mcp

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
)

func TestAllConfigurationArgumentsAreFlat(t *testing.T) {
	for _, tool := range Tools() {
		def := tool.(map[string]any)
		props := def["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if props["config"] != nil {
			t.Fatalf("nested config exposed by %s", def["name"])
		}
		if props["encoding"] != nil {
			if props["max_output_bytes"] == nil {
				t.Fatal(def)
			}
			if def["name"] != "connect" && def["name"] != "configure" && props["read_only"] != nil {
				t.Fatal("operation exposes read_only", def)
			}
		}
	}
}

func TestFlatConnectConfigureSendReadAndJumpThroughMCP(t *testing.T) {
	c := harness(t)
	sequence := 0
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		sequence++
		id := strconv.Itoa(sequence)
		c.tool(t, id, tool, args)
		return structured(t, c.receive(t, id, 3*time.Second))
	}
	target, _ := testutil.SSH(t)
	jump, key, _ := testutil.SSHJump(t, target, false)
	jargs := connectArgs(jump)
	delete(jargs, "protocol")
	jargs["username"], jargs["password"], jargs["host_key"] = "jump-user", "jump-secret", key
	args := connectArgs(target)
	args["protocol"], args["username"], args["password"], args["mode"] = "ssh", "test", "test", "stream"
	args["ssh_jumps"] = []any{jargs}
	args["read_idle_timeout_ms"], args["encoding"], args["max_output_bytes"] = 20, "utf-8", 4096
	v := call("connect", args)
	id := v["session"]
	if v["connected"] != true {
		t.Fatal(v)
	}
	v = call("configure", map[string]any{"session": id, "read_only": true, "line_end": "\\n", "max_output_bytes": "8*1024"})
	conf := v["config"].(map[string]any)
	if conf["read_only"] != true || conf["max_output_bytes"] != float64(8192) {
		t.Fatal(v)
	}
	v = call("send_text", map[string]any{"session": id, "data": "must-not-send"})
	if v["input_state"] != "not_sent" || !strings.HasPrefix(v["error"].(string), "read_only:") {
		t.Fatal(v)
	}
	for _, value := range []bool{true, false} {
		v = call("send_text", map[string]any{"session": id, "data": "must-not-send", "read_only": value})
		if !strings.Contains(v["error"].(string), "read_only") {
			t.Fatal(v)
		}
	}
	call("configure", map[string]any{"session": id, "read_only": false})
	for _, tool := range []string{"send_text", "send_escaped", "sendline_text", "sendline_escaped"} {
		v = call(tool, map[string]any{"session": id, "data": "display version", "line_end": "\\r", "collect_output": false, "max_input_bytes": 4096})
		if v["input_state"] != "sent" {
			t.Fatal(v)
		}
	}
	for _, tool := range []string{"read_available", "read_until", "read_tail", "read_history"} {
		v = call(tool, map[string]any{"session": id, "read_total_timeout_ms": 20, "read_idle_timeout_ms": 5, "max_output_bytes": 4096, "end_pattern": ""})
		if v["error"] != nil || v["session"] != id {
			t.Fatal(v)
		}
	}
	conf = call("get_config", map[string]any{"session": id})["config"].(map[string]any)
	if conf["max_output_bytes"] != float64(8192) || conf["read_idle_timeout_ms"] != float64(20) || conf["line_end"] != "\\n" {
		t.Fatal(conf)
	}
	call("close", map[string]any{"session": id})
}
