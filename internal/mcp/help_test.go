package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
)

func TestHelpDirectoryAndAllParameterDetails(t *testing.T) {
	directory, err := builtinHelp(helpRequest{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(directory)
	if len(b) > 16000 {
		t.Fatal("directory unexpectedly verbose", len(b))
	}
	for _, tool := range Tools() {
		def := tool.(map[string]any)
		name := def["name"].(string)
		if def["description"] == "" {
			t.Fatal("missing tool purpose", name)
		}
		full, err := builtinHelp(helpRequest{Topic: name})
		if err != nil {
			t.Fatal(name, err)
		}
		encoded, _ := json.Marshal(full)
		if len(encoded) > 24576 {
			t.Fatal("unbounded tool help", name, len(encoded))
		}
		for key, field := range def["inputSchema"].(map[string]any)["properties"].(map[string]any) {
			result, err := builtinHelp(helpRequest{Topic: name, Parameter: key})
			if err != nil {
				t.Fatal(name, key, err)
			}
			schema := result.(map[string]any)["schema"].(map[string]any)
			if schema["description"] == nil {
				t.Fatal("missing parameter documentation", name, key)
			}
			if expected, hasDefault := field.(map[string]any)["default"]; hasDefault {
				x, _ := json.Marshal(expected)
				y, _ := json.Marshal(schema["default"])
				if string(x) != string(y) {
					t.Fatal("default mismatch", name, key, string(x), string(y))
				}
			}
		}
	}
	result, err := builtinHelp(helpRequest{Topic: "configuration", Parameter: "read_total_timeout_ms"})
	if err != nil || result.(map[string]any)["schema"].(map[string]any)["default"] != float64(30000) {
		t.Fatal(result, err)
	}
}

func TestHelpSearchRankingBoundsAndNoMatch(t *testing.T) {
	for _, tc := range []struct{ query, topic, parameter string }{
		{"RPC 超时", "timeouts", ""}, {"环境变量", "exec", "env"}, {"跳板 telnet", "ssh_jumps", ""}, {"read_only", "configuration", "read_only"},
	} {
		hits := searchHelp(tc.query, 3)
		if len(hits) == 0 {
			t.Fatal(tc.query)
		}
		found := false
		for _, hit := range hits {
			if hit.Topic == tc.topic && hit.Parameter == tc.parameter {
				found = true
			}
			if len([]rune(hit.Summary)) > 222 {
				t.Fatal("snippet too long", hit)
			}
			if _, err := builtinHelp(helpRequest{Topic: hit.Topic, Parameter: hit.Parameter}); err != nil {
				t.Fatal("unexpandable search hit", hit, err)
			}
		}
		if !found {
			t.Fatal("expected relevant search hit", tc, hits)
		}
		b, _ := json.Marshal(hits)
		if len(b) > 5000 {
			t.Fatal("search output too large", len(b))
		}
	}
	if hits := searchHelp("zzzz_unfindable_credential_value", 3); len(hits) != 0 {
		t.Fatal(hits)
	}
	for _, r := range []helpRequest{{Query: strings.Repeat("a", 513)}, {Topic: "unknown"}, {Parameter: "env"}, {Topic: "exec", Query: "env"}, {Topic: "exec", Parameter: "read_only"}, {Topic: "timeouts", Parameter: "env"}, {Limit: intPointer(6)}} {
		if _, err := builtinHelp(r); err == nil {
			t.Fatal("invalid help accepted", r)
		}
	}
}

func intPointer(n int) *int { return &n }

func TestHelpReadOnlyStandaloneAndDiscoveryIsolation(t *testing.T) {
	// Help has no dependency on a Manager, transport, filesystem or network.
	s := &Server{}
	request, _ := json.Marshal(map[string]any{"name": "help", "arguments": map[string]any{"query": "env"}})
	result := s.call(context.Background(), request).(map[string]any)
	if result["isError"] != false {
		t.Fatal(result)
	}
	before, _ := json.Marshal(definitions)
	_ = buildPublicDefinitions(definitions)
	_, _ = builtinHelp(helpRequest{Topic: "connect", Parameter: "ssh_jumps"})
	after, _ := json.Marshal(definitions)
	if string(before) != string(after) {
		t.Fatal("discovery/help mutated runtime definitions")
	}
	for _, name := range []string{"read_available", "read_tail", "read_history"} {
		props := publicDefinitions[name]["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if props["read_total_timeout_ms"] != nil || props["write_timeout_ms"] != nil {
			t.Fatal("inapplicable settings exposed", name)
		}
		if props["max_output_bytes"] == nil || props["encoding"] == nil || props["include_pattern"] == nil {
			t.Fatal("meaningful settings hidden", name)
		}
	}
	props := publicDefinitions["exec"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if props["read_total_timeout_ms"] != nil || props["timeout_ms"] == nil || props["login"].(map[string]any)["default"] != true {
		t.Fatal("exec discovery semantics lost", props)
	}
}

func TestStdioProgressiveHelpAndCommandWorkflow(t *testing.T) {
	c := harness(t)
	c.tool(t, "search", "help", map[string]any{"query": "RPC 超时"})
	result := structured(t, c.receive(t, "search", time.Second))
	if result["matched"] != true || len(result["results"].([]any)) > 3 {
		t.Fatal(result)
	}
	c.tool(t, "field", "help", map[string]any{"topic": "exec", "parameter": "login"})
	result = structured(t, c.receive(t, "field", time.Second))
	if result["schema"].(map[string]any)["default"] != true {
		t.Fatal(result)
	}
	addr, _ := testutil.SSH(t)
	args := connectArgs(addr)
	args["protocol"], args["username"], args["password"] = "ssh", "test", "test"
	c.tool(t, "connect", "connect", args)
	id := structured(t, c.receive(t, "connect", time.Second))["session"]
	c.tool(t, "run", "exec", map[string]any{"session": id, "command": "both", "collect_output": false})
	handle := structured(t, c.receive(t, "run", time.Second))["handle"]
	c.tool(t, "wait", "task_wait", map[string]any{"handle": handle, "exec_total_timeout_ms": 1000, "term_on_timeout": false})
	result = structured(t, c.receive(t, "wait", 2*time.Second))
	if result["reason"] != "exec_finish" || result["exit_code"] != float64(0) || !strings.Contains(result["output"].(string), "stdout") || !strings.Contains(result["output"].(string), "stderr") {
		t.Fatal(result)
	}
}
