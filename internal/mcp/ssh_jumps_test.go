package mcp

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
)

func TestSSHJumpsNameAndTelnetTarget(t *testing.T) {
	c := harness(t)
	sequence := 0
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		sequence++
		id := strconv.Itoa(sequence)
		c.tool(t, id, tool, args)
		return structured(t, c.receive(t, id, 3*time.Second))
	}
	props := definitions["connect"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if props["ssh_jumps"] == nil || props["jumps"] != nil {
		t.Fatal("wrong public jump parameter", props)
	}
	for _, both := range []bool{false, true} {
		args := map[string]any{"protocol": "telnet", "host": "unused", "port": 23, "jumps": []any{}}
		if both {
			args["ssh_jumps"] = []any{}
		}
		v := call("connect", args)
		if !strings.Contains(v["error"].(string), `unknown argument "jumps"`) {
			t.Fatal(v)
		}
	}
	target := testutil.TelnetSwitch(t)
	addr, key, _ := testutil.SSHJump(t, target, false)
	jump := connectArgs(addr)
	delete(jump, "protocol")
	jump["username"], jump["password"], jump["host_key"] = "jump-user", "jump-secret", key
	args := connectArgs(target)
	args["ssh_jumps"] = []any{jump}
	args["read_idle_timeout_ms"] = 0
	args["read_total_timeout_ms"] = 2000
	v := call("connect", args)
	id := v["session"]
	if v["connected"] != true || v["peer"] != nil {
		t.Fatal(v)
	}
	v = call("read_until", map[string]any{"session": id, "end_pattern": "switch> "})
	if v["reason"] != "match" {
		t.Fatal(v)
	}
	v = call("exec", map[string]any{"session": id, "command": "must-not-run"})
	if !strings.Contains(v["error"].(string), "requires SSH mode=exec") {
		t.Fatal(v)
	}
	v = call("sendline_text", map[string]any{"session": id, "data": "show version", "end_pattern": "switch> "})
	if v["input_state"] != "sent" || !strings.Contains(v["output"].(string), "SwitchOS") {
		t.Fatal(v)
	}
	v = call("reconnect", map[string]any{"session": id})
	if v["connected"] != true || v["session"] != id || v["peer"] != nil {
		t.Fatal(v)
	}
	call("close", map[string]any{"session": id})
	args["mode"] = "exec"
	v = call("connect", args)
	if !strings.Contains(v["error"].(string), "Telnet requires stream") {
		t.Fatal(v)
	}
}
