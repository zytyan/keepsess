package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zytyan/keepsess/internal/core"
)

func TestExplicitAliasVocabularyAndConflicts(t *testing.T) {
	for tool, def := range definitions {
		props := def["inputSchema"].(map[string]any)["properties"].(map[string]any)
		for alias, canonical := range AcceptedParameterAliases(tool) {
			value := json.RawMessage(`"sample"`)
			if strings.HasSuffix(canonical, "_ms") {
				value = json.RawMessage(`10`)
			}
			keys := map[string]json.RawMessage{alias: value}
			if err := normalizeAliases(tool, props, keys, false); err != nil || string(keys[canonical]) != string(value) || keys[alias] != nil {
				t.Fatalf("%s.%s -> %s: %v %v", tool, alias, canonical, keys, err)
			}
			keys = map[string]json.RawMessage{alias: value, canonical: value}
			if strings.HasSuffix(canonical, "_ms") {
				keys[alias] = json.RawMessage(`"2*5"`)
			}
			if err := normalizeAliases(tool, props, keys, false); err != nil {
				t.Fatal(tool, alias, err)
			}
			keys = map[string]json.RawMessage{alias: json.RawMessage(`"different"`), canonical: value}
			if err := normalizeAliases(tool, props, keys, false); err == nil {
				t.Fatal("conflict accepted", tool, alias)
			}
		}
	}
}

func TestAliasLayeringAndPreferredDiscovery(t *testing.T) {
	s := &Server{Manager: core.NewManager()}
	defer s.Manager.CloseAll()
	for _, tool := range []string{"exec", "task_wait"} {
		args := map[string]any{"timeout_ms": 10, "execution_timeout_ms": 11, "command": "secret", "handle": "task1"}
		if got := directCall(t, s, tool, args); !strings.Contains(got["error"].(string), "conflicting arguments") {
			t.Fatal(got)
		}
	}
	for _, tool := range []string{"connect", "configure", "tunnel_close"} {
		args := map[string]any{"timeout_ms": 10}
		if tool == "tunnel_close" {
			args = map[string]any{"task_id": "tunnel1"}
		}
		if got := directCall(t, s, tool, args); !strings.Contains(got["error"].(string), "unknown argument") {
			t.Fatal(got)
		}
	}
	for _, raw := range Tools() {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		for _, group := range parameterAliases(name) {
			if props[group.preferred] == nil {
				continue
			}
			for _, other := range append([]string{group.canonical}, group.aliases...) {
				if other != group.preferred && props[other] != nil {
					t.Fatal("duplicate discovered", name, other)
				}
			}
		}
	}
	// Normalize the two layers independently; the server merges top-level last.
	props := definitions["exec"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	nested := map[string]json.RawMessage{"exec_timeout_ms": json.RawMessage(`1`)}
	top := map[string]json.RawMessage{"execution_timeout_ms": json.RawMessage(`20`)}
	if err := normalizeAliases("exec", props, nested, true); err != nil {
		t.Fatal(err)
	}
	if err := normalizeAliases("exec", props, top, false); err != nil {
		t.Fatal(err)
	}
	for k, v := range top {
		nested[k] = v
	}
	if string(nested["exec_total_timeout_ms"]) != "20" {
		t.Fatal(nested)
	}
}
