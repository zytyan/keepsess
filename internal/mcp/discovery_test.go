package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Check public tool definitions: parameters must be usable without
// first loading help, including nested jump authentication settings.
func TestDiscoveryIncludesCompleteDocumentation(t *testing.T) {
	var checkFields func(map[string]any, string)
	checkFields = func(node map[string]any, path string) {
		if props, ok := node["properties"].(map[string]any); ok {
			for name, value := range props {
				field := value.(map[string]any)
				if desc, ok := field["description"].(string); !ok || strings.TrimSpace(desc) == "" {
					t.Errorf("missing parameter description: %s.%s", path, name)
				}
				checkFields(field, path+"."+name)
			}
		}
		if item, ok := node["items"].(map[string]any); ok {
			checkFields(item, path+"[]")
		}
	}
	for _, value := range Tools() {
		tool := value.(map[string]any)
		name := tool["name"].(string)
		help, err := builtinHelp(helpRequest{Topic: name})
		if err != nil {
			t.Fatal(err)
		}
		detail := help.(map[string]any)
		if tool["description"] != detail["description"] {
			t.Errorf("%s shortened tool description", name)
		}
		got, _ := json.Marshal(tool["inputSchema"])
		want, _ := json.Marshal(detail["inputSchema"])
		if string(got) != string(want) {
			t.Errorf("%s discovery lost parameter details or constraints", name)
		}
		checkFields(tool["inputSchema"].(map[string]any), name)
	}
}

func TestDiscoveryDocumentsFirstCallBoundaries(t *testing.T) {
	for _, tc := range []struct {
		tool, parameter string
		fragments       []string
	}{
		{"exec", "session", []string{"exactly one", "disconnected/stream", "multiple sessions"}},
		{"exec", "command", []string{"shell-quoting", "wrapper", "max_input_bytes"}},
		{"exec", "login", []string{"bash --login -c", "without fallback", "cwd/env"}},
		{"exec", "collect_output", []string{"exec_total_timeout_ms", "False returns a task handle", "no execution wait"}},
		{"exec", "exec_total_timeout_ms", []string{"300000ms", "RPC budget", "inherit the session"}},
		{"send_escaped", "data", []string{"AFTER JSON decoding", "DSML", "Invalid escapes reject before writing", "configured encoding"}},
		{"sendline_text", "data", []string{"no escape decoding", "exactly once", "Empty data allowed"}},
		{"sendline_text", "line_end", []string{"decoded once", "JSON", "Defaults: SSH", "Telnet"}},
		{"read_until", "include_pattern", []string{"RE2", "1024 bytes", "64KiB", "advances the cursor"}},
		{"configure", "read_only", []string{"not a security boundary", "does not stop"}},
	} {
		props := publicDefinitions[tc.tool]["inputSchema"].(map[string]any)["properties"].(map[string]any)
		desc := props[tc.parameter].(map[string]any)["description"].(string)
		for _, fragment := range tc.fragments {
			if !strings.Contains(desc, fragment) {
				t.Errorf("%s.%s missing %q", tc.tool, tc.parameter, fragment)
			}
		}
	}
}
