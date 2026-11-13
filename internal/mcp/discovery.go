package mcp

import (
	"encoding/json"
	"strings"
)

// Detailed definitions remain the runtime compatibility contract and help source.
// Discovery is an independent projection: never mutate the shared rich schemas.
var publicDefinitions = buildPublicDefinitions(definitions)

var toolOrder = []string{"get_file", "put_file", "tunnel_open", "tunnel_list", "tunnel_close", "help", "connect", "reconnect", "rename", "task_rename", "configure", "get_config", "list", "exec", "task_list", "task_status", "task_wait", "task_signal", "task_release", "task_close_stdin", "send_text", "send_escaped", "sendline_text", "sendline_escaped", "read_available", "read_until", "read_tail", "read_history", "close", "close_all"}

const residentInstructions = `keepsess API contract 2. Tools are connect/exec/read_available/etc., without sess_; clients may add keepsess_. All 30 schemas include complete parameter details; help is optional. Parameters are top-level.
Choose before connect: SSH servers -> mode=exec (default), noninteractive Bash login shell; network devices/console shells/Telnet -> mode=stream, sendline_text then read. Never auto-switch mode/replay. interactive=true retains exec stdin; pty=true requires interactive. task_close_stdin sends SSH EOF.
References: session accepts sN OR current session name; handle accepts taskN OR current task name. name only assigns/renames, never selects. Renaming cannot use sN/taskN patterns. Stream send/read uses session; exec task send/read uses handle; never both. exec can omit session only with exactly one retained session, including disconnected/stream. Unknown reference -> list/task_list, check instance_id; no guessing or replay. connect(profile="NAME") explicitly creates a NEW connection from configured profiles; no automatic last/restore.
Core defaults: exec(command,timeout_ms=120000,max_output_bytes=65536,term_on_timeout=true); task_signal(handle,signal); sendline_text(session|handle,data,collect_output=true). Byte limits are not token limits. send_escaped decodes one visible-escape layer without adding Enter; sendline adds line_end once.
output_limit/scan_limit -> continue same reference using next_action/next_arguments; never rerun input. busy -> let active operation return, no polling exec. start/end are raw offsets, more means unread retained bytes; dropped_bytes is irreversible history loss. RPC budget expiry keeps handle without TERM; continue wait/read. Only configured exec completion timeout may attempt TERM. Missing exit status is unknown outcome.
Never replay on silence/timeouts; never infer execution/authentication success/failure from empty output. sent/written_bytes confirm local writing only. read_only is set only by connect/configure. After first successful connect use configure(instance_name="work label"). initialize._meta/list expose instance_id, api_contract, available_profiles and Web status. Long workflows: help(topic=tool_or_topic,parameter=optional_field) or query.`

// Prune only inapplicable configuration from discovery, not accepted runtime
// arguments. In particular, legacy nested config and existing ignored overrides
// retain their old behavior. All meaningful options remain discoverable.
func applicableConfig(tool, key string) bool {
	if tool == "connect" || tool == "configure" {
		return true
	}
	switch key {
	case "encoding", "output_decode_error_action", "max_output_bytes", "strip_ansi", "include_pattern", "exclude_pattern":
		return tool == "exec" || tool == "task_wait" || strings.HasPrefix(tool, "read_") || strings.HasPrefix(tool, "send")
	case "read_total_timeout_ms", "read_idle_timeout_ms", "end_pattern":
		return tool == "read_until" || strings.HasPrefix(tool, "send")
	case "exec_total_timeout_ms", "term_on_timeout":
		return tool == "exec" || tool == "task_wait"
	case "input_encode_error_action", "max_input_bytes", "backpressure_bytes", "write_timeout_ms":
		return tool == "exec" || strings.HasPrefix(tool, "send")
	case "line_end":
		return strings.HasPrefix(tool, "sendline_")
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
	for name, original := range full {
		def := cloneMap(original)
		input := def["inputSchema"].(map[string]any)
		input["properties"] = preferredProperties(name, input["properties"].(map[string]any))
		out[name] = def
	}
	return out
}
