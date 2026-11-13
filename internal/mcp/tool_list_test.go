package mcp

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAllToolsDiscoverableWithoutProfile(t *testing.T) {
	c := harness(t)
	c.send(t, "init", "initialize", map[string]any{})
	init := c.receive(t, "init", time.Second)["result"].(map[string]any)
	capabilities := init["capabilities"].(map[string]any)["tools"].(map[string]any)
	if capabilities["listChanged"] == true {
		t.Fatal("fixed tool list advertises dynamic discovery")
	}
	list := func(id string) []any {
		t.Helper()
		c.send(t, id, "tools/list", map[string]any{})
		return c.receive(t, id, time.Second)["result"].(map[string]any)["tools"].([]any)
	}
	initial := list("initial")
	if len(initial) != 30 {
		t.Fatal("all tools must be immediately discoverable", len(initial))
	}
	got, _ := json.Marshal(initial)
	want, _ := json.Marshal(Tools())
	if string(got) != string(want) {
		t.Fatal("tools/list differs from complete tool definitions")
	}
	props := publicDefinitions["configure"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if props["tool_profile"] != nil {
		t.Fatal("obsolete profile switch exposed")
	}
	c.tool(t, "name", "configure", map[string]any{"instance_name": "test work"})
	r := structured(t, c.receive(t, "name", time.Second))
	if r["instance_name"] != "test work" || r["tool_profile"] != nil {
		t.Fatal(r)
	}
	after, _ := json.Marshal(list("after-name"))
	if string(after) != string(got) {
		t.Fatal("naming changed discovery")
	}
	c.tool(t, "obsolete", "configure", map[string]any{"tool_profile": "standard"})
	if c.receive(t, "obsolete", time.Second)["result"].(map[string]any)["isError"] != true {
		t.Fatal("obsolete profile switch still accepted")
	}
}
