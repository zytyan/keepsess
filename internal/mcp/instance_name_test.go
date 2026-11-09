package mcp

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
)

func TestFirstSuccessfulConnectNamesInstanceThroughConfigure(t *testing.T) {
	c := harness(t)
	sequence := 0
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		sequence++
		id := strconv.Itoa(sequence)
		c.tool(t, id, tool, args)
		return structured(t, c.receive(t, id, 3*time.Second))
	}
	// Failed validation must not consume the first-success naming prompt.
	call("sess_connect", map[string]any{"protocol": "ssh", "host": "127.0.0.1", "port": 0})
	addr, _ := testutil.SSH(t)
	first := call("sess_connect", connectArgs(addr))
	if first["next_action"] != "sess_configure" || !strings.Contains(first["comment"].(string), "instance_name") {
		t.Fatal(first)
	}
	named := call("sess_configure", map[string]any{"instance_name": "现场网络诊断"})
	if named["instance_id"] != first["instance_id"] || named["instance_name"] != "现场网络诊断" {
		t.Fatal(named)
	}
	listed := call("sess_list", map[string]any{})
	if listed["instance_name"] != "现场网络诊断" || len(listed["sessions"].([]any)) != 1 {
		t.Fatal(listed)
	}
	// Instance naming cannot partially apply a session patch.
	mixed := call("sess_configure", map[string]any{"instance_name": "changed", "session": first["session"], "read_only": true})
	if mixed["error"] == nil {
		t.Fatal(mixed)
	}
	still := call("sess_list", map[string]any{})
	if still["instance_name"] != "现场网络诊断" {
		t.Fatal(still)
	}
	second := call("sess_connect", connectArgs(addr))
	if second["comment"] != nil || second["next_action"] != nil || second["instance_name"] != "现场网络诊断" {
		t.Fatal(second)
	}
	for _, bad := range []any{"", " leading", "line\nbreak", strings.Repeat("中", 65), nil} {
		v := call("sess_configure", map[string]any{"instance_name": bad})
		if v["error"] == nil {
			t.Fatal("invalid name accepted", v)
		}
	}
	if len(Tools()) != 30 {
		t.Fatal("naming added a tool")
	}
}
