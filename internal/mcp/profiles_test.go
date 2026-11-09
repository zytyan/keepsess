package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStandardFullDiscoveryAndCompatibleCalls(t *testing.T) {
	c := harness(t)
	list := func(id string) []any {
		t.Helper()
		c.send(t, id, "tools/list", map[string]any{})
		return c.receive(t, id, time.Second)["result"].(map[string]any)["tools"].([]any)
	}
	initial := list("initial")
	if len(initial) != 17 {
		t.Fatal(len(initial))
	}
	have := map[string]bool{}
	for _, v := range initial {
		have[v.(map[string]any)["name"].(string)] = true
	}
	for _, key := range []string{"sess_help", "sess_configure", "sess_exec", "sess_send_text", "sess_read_until", "sess_task_wait", "sess_task_signal"} {
		if !have[key] {
			t.Fatal("missing essential tool", key)
		}
	}
	if have["sess_tunnel_open"] || have["sess_send_escaped"] || have["sess_task_release"] {
		t.Fatal("advanced discovery leaked")
	}
	c.tool(t, "expand", "sess_configure", map[string]any{"tool_profile": "full"})
	r := structured(t, c.receive(t, "expand", time.Second))
	if r["tool_count"] != float64(30) || r["tool_profile"] != "full" {
		t.Fatal(r)
	}
	if got := list("expanded"); len(got) != 30 {
		t.Fatal(len(got))
	}
	// The notification may precede the reply, but it must arrive once after a
	// real profile change; clients can refresh their registered tool functions.
	notification := c.receive(t, "", time.Second)
	if notification["method"] != "notifications/tools/list_changed" {
		t.Fatal(notification)
	}
	c.tool(t, "compact", "sess_configure", map[string]any{"tool_profile": "standard"})
	structured(t, c.receive(t, "compact", time.Second))
	c.receive(t, "", time.Second)
	if len(list("compact-list")) != 17 {
		t.Fatal("did not restore standard")
	}
	// Runtime compatibility includes tools omitted from the current discovery.
	c.tool(t, "legacy", "sess_task_status", map[string]any{"handle": "task999"})
	unknown := structured(t, c.receive(t, "legacy", time.Second))
	if strings.Contains(unknown["error"].(string), "unknown tool") {
		t.Fatal(unknown)
	}
	b1, _ := json.Marshal(initial)
	b2, _ := json.Marshal(Tools())
	t.Logf("tools/list: standard %d tools / %d bytes; full %d tools / %d bytes", len(initial), len(b1), len(Tools()), len(b2))
}
