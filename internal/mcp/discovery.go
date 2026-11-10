package mcp

import (
	"encoding/json"
	"strings"

	"github.com/zytyan/keepsess/internal/core"
)

// Detailed definitions remain the runtime compatibility contract and help source.
// Discovery is an independent projection: never mutate the shared rich schemas.
var publicDefinitions = buildPublicDefinitions(definitions)

var toolOrder = []string{"sess_get_file", "sess_put_file", "sess_tunnel_open", "sess_tunnel_list", "sess_tunnel_close", "sess_help", "sess_connect", "sess_reconnect", "sess_rename", "sess_task_rename", "sess_configure", "sess_get_config", "sess_list", "sess_exec", "sess_task_list", "sess_task_status", "sess_task_wait", "sess_task_signal", "sess_task_release", "sess_task_close_stdin", "sess_send_text", "sess_send_escaped", "sess_sendline_text", "sess_sendline_escaped", "sess_read_available", "sess_read_until", "sess_read_tail", "sess_read_history", "sess_close", "sess_close_all"}

const residentInstructions = `All 30 tools are discoverable with complete descriptions; no discovery profiles or expansion step. sess_send_escaped decodes once and appends no line ending; include CR/LF explicitly. To inspect settings use sess_configure({"session":"s1"}) without patches. After the first successful connect, name this Agent instance for its current work using sess_configure({"instance_name":"short task label"}) without session; do not reconnect. Human Web URL/status is available in initialize._meta.web and sess_list.web; browser input defaults read-only and history includes terminal input/output. Persistent SSH/Telnet sessions; all parameters are top-level. Use exec for SSH server commands; SSH devices/console shells and all Telnet use stream send/read. Exec defaults to a noninteractive Bash login shell, stdin EOF; interactive=true retains stdin, pty requires interactive. Stream writes confirm only local writing, never login/delivery. Silence or timeouts never justify replaying commands/credentials or adding Enter. Session and task IDs/names are process-local; unknown handles: inspect lists. Exec tasks use handle for reads/input, streams use session, never both. Exec/task_wait use independent completion waits; RPC budget expiry retains handle without TERM: continue wait/read. Only configured completion timeout may attempt TERM; it never guarantees exit. Missing exit status means unknown outcome. Normal read timeouts preserve connections and never signal. Read_only blocks application input/signals, only connect/configure may change it. Each discovered tool includes its full parameter documentation and constraints; use it directly without a preliminary help call. sess_help(topic=tool_or_topic, parameter=optional_name) or query provides optional workflows and troubleshooting.`

// Prune only inapplicable configuration from discovery, not accepted runtime
// arguments. In particular, legacy nested config and existing ignored overrides
// retain their old behavior. All meaningful options remain discoverable.
func applicableConfig(tool, key string) bool {
	if tool == "sess_connect" || tool == "sess_configure" {
		return true
	}
	switch key {
	case "encoding", "output_decode_error_action", "max_output_bytes", "strip_ansi", "include_pattern", "exclude_pattern":
		return tool == "sess_exec" || tool == "sess_task_wait" || strings.HasPrefix(tool, "sess_read_") || strings.HasPrefix(tool, "sess_send")
	case "read_total_timeout_ms", "read_idle_timeout_ms", "end_pattern":
		return tool == "sess_read_until" || strings.HasPrefix(tool, "sess_send")
	case "exec_total_timeout_ms", "term_on_timeout":
		return tool == "sess_exec" || tool == "sess_task_wait"
	case "input_encode_error_action", "max_input_bytes", "backpressure_bytes", "write_timeout_ms":
		return tool == "sess_exec" || strings.HasPrefix(tool, "sess_send")
	case "line_end":
		return strings.HasPrefix(tool, "sess_sendline_")
	}
	return false
}

func cloneMap(value map[string]any) map[string]any {
	b, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// Keep the complete tool and field documentation in discovery. Clients can load
// individual tools on demand; requiring a second help call loses startup and
// escaping constraints exactly when a model needs them to construct a call.
func buildPublicDefinitions(full map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(full))
	configKeys := core.ConfigDescriptions()
	for name, original := range full {
		def := cloneMap(original)
		input := def["inputSchema"].(map[string]any)
		props := input["properties"].(map[string]any)
		for key, field := range props {
			if _, config := configKeys[key]; config && !applicableConfig(name, key) {
				delete(props, key)
				continue
			}
			props[key] = documentedField(key, field.(map[string]any))
		}
		out[name] = def
	}
	return out
}
