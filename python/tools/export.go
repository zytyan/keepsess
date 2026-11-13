// Export the complete accepted argument surface for the Python SDK generator.
// Discovery hides ignored options, but SDK compatibility must retain them.
package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/mcp"
)

func main() {
	tools := mcp.Tools()
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		schema := tool["inputSchema"].(map[string]any)
		props := schema["properties"].(map[string]any)
		if name != "connect" && name != "configure" && name != "exec" && name != "task_wait" && !strings.HasPrefix(name, "send") && !strings.HasPrefix(name, "read_") {
			continue
		}
		for key, desc := range core.ConfigDescriptions() {
			if name != "connect" && name != "configure" {
				switch key {
				case "connect_timeout_ms", "session_idle_timeout_ms", "tcp_keepalive_ms", "ssh_keepalive_ms", "ssh_keepalive_timeout_ms", "read_only":
					continue
				}
			}
			kind := any("string")
			if key == "strip_ansi" || key == "term_on_timeout" || key == "read_only" {
				kind = "boolean"
			} else if strings.HasSuffix(key, "_ms") || strings.HasSuffix(key, "_bytes") {
				kind = []string{"integer", "string"}
			}
			props[key] = map[string]any{"type": kind, "description": desc}
		}
		props["config"] = map[string]any{"type": "object", "description": "Legacy nested configuration; prefer top-level arguments. Explicit top-level values win."}
	}
	if err := json.NewEncoder(os.Stdout).Encode(tools); err != nil {
		panic(err)
	}
}
