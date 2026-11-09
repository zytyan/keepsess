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

const residentInstructions = `Default discovery lists 18 daily tools, including sess_send_escaped (decode once, no appended line ending; include CR/LF explicitly). For tunnels, escaped sendline, raw history or alias/retention tools, call sess_configure({"tool_profile":"full"}) without session and refresh tools/list; use standard to return. To inspect settings use sess_configure({"session":"s1"}) without patches. After the first successful connect, name this Agent instance for its current work using sess_configure({"instance_name":"short task label"}) without session; do not reconnect. Human Web URL/status is available in initialize._meta.web and sess_list.web; browser input defaults read-only and history includes terminal input/output. Persistent SSH/Telnet sessions; all parameters are top-level. Use exec for SSH server commands; SSH devices/console shells and all Telnet use stream send/read. Exec defaults to a noninteractive Bash login shell, stdin EOF; interactive=true retains stdin, pty requires interactive. Stream writes confirm only local writing, never login/delivery. Silence or timeouts never justify replaying commands/credentials or adding Enter. Session and task IDs/names are process-local; unknown handles: inspect lists. Exec tasks use handle for reads/input, streams use session, never both. Exec/task_wait use independent completion waits; RPC budget expiry retains handle without TERM: continue wait/read. Only configured completion timeout may attempt TERM; it never guarantees exit. Missing exit status means unknown outcome. Normal read timeouts preserve connections and never signal. Read_only blocks application input/signals, only connect/configure may change it. For parameter details, workflows, defaults and troubleshooting use sess_help(topic=tool_or_topic, parameter=optional_name), or query for short search results; help is optional for routine calls.`

var briefTools = map[string]string{
	"sess_help":             "Built-in help: no arguments lists topics; topic/parameter expands details; query searches short snippets (default 3). No files, sessions or network accessed.",
	"sess_connect":          "Create persistent SSH/Telnet session. SSH defaults exec; network devices/shells need stream, Telnet always stream. SSH host-key checking defaults OFF unless host_key/known_hosts supplied. No automatic Telnet login.",
	"sess_reconnect":        "Explicitly reconnect with saved private parameters; preserve session ID/name. Discards old output/tasks; never replays input. Busy during active operations.",
	"sess_rename":           "Rename a session alias; stable sN ID remains valid. Old alias stops resolving. Duplicate names rejected.",
	"sess_task_rename":      "Rename a task alias; stable taskN ID remains valid. Names unique across retained tasks. No output consumed.",
	"sess_configure":        "Without session: set instance_name (Unicode label) or tool_profile (standard/full). With session alone: inspect settings; with patches: update top-level session defaults atomically; omitted fields unchanged. read_only changes return busy during application writes.",
	"sess_get_config":       "Inspect effective session settings, field descriptions and platform capabilities; no credentials.",
	"sess_list":             "List retained sessions, names and connection state plus process instance_id; consumes nothing.",
	"sess_exec":             "SSH mode=exec only. Run command with protocol exit status; Bash login by default, stdin EOF. Sync waits by default; collect_output=false starts async and returns task handle. Early returns retain handle: continue wait/read, never replay. Ignores read timeouts/end_pattern.",
	"sess_task_list":        "List retained tasks, optionally by session; includes completed records and handles omitted by normal sync completion. No output consumed.",
	"sess_task_status":      "Inspect task state, exit/warnings, age, stdin state and unread output; consumes nothing. Finished channel without exit status still has unknown outcome.",
	"sess_task_wait":        "Wait again on an existing task, collecting incremental output with a fresh completion budget. Returns elapsed time/timeout_source. RPC/caller deadline never sends TERM.",
	"sess_task_signal":      "Request SSH signal, guarded by read_only. sent confirms request write only, not exit. No automatic KILL or retry.",
	"sess_task_release":     "Release a completed idle task and its output. Running, unknown or active records are rejected.",
	"sess_task_close_stdin": "Send SSH EOF to interactive task stdin; idempotent, not Ctrl-D, not termination. No output consumed; read_only guarded.",
	"sess_send_text":        "Send literal text, optionally collecting unread output (default true). Local write receipt only; empty output is not delivery/login evidence.",
	"sess_send_escaped":     "Decode one layer of visible escapes then send; no appended line ending (include CR/LF explicitly). Invalid escapes send nothing. Collects unread output by default. Never retry on silence.",
	"sess_sendline_text":    "Send literal text plus one configured line ending, collecting output by default. Local write receipt only; never add Enter/replay on silence.",
	"sess_sendline_escaped": "Decode visible escapes, append one line ending, send and collect by default. Invalid escapes send nothing; never retry on silence.",
	"sess_read_available":   "Immediately collect unread output, without waiting. Partial characters/ANSI/filtered lines may remain unread. Never sends signals.",
	"sess_read_until":       "Collect until pattern, total/idle timeout or output/state boundary. Timeout preserves connection and never sends TERM. Pattern does not prove execution/login success.",
	"sess_read_tail":        "Read last N retained raw lines (default 20), without moving incremental cursor. Reports history loss/output bounds.",
	"sess_read_history":     "Page retained raw history from offset; does not move incremental cursor. Use returned end as next offset; reports dropped bytes.",
	"sess_close":            "Close session; discard all buffers/tasks/private reconnect parameters. Remote programs may keep running. No output returned.",
	"sess_close_all":        "Close every session and discard buffers/tasks/private parameters; cancels pending connects/reconnects. Remote programs may keep running.",
}

var briefParameters = map[string]string{
	"protocol":              "Final target transport; all ssh_jumps use SSH.",
	"host":                  "Final target hostname/IP; IPv6 without brackets.",
	"port":                  "Final target TCP port.",
	"username":              "SSH target username; Telnet login uses explicit stream input.",
	"password":              "SSH password; audit redacted.",
	"keyfile":               "Local SSH private-key path.",
	"key_passphrase":        "Private-key passphrase; audit redacted.",
	"topic":                 "Tool name or directory topic; omit for index.",
	"parameter":             "Optional single field within topic.",
	"query":                 "English/Chinese help search; exclusive with topic/parameter.",
	"limit":                 "Search results 1..5, default 3.",
	"session":               "Session ID/name. Required for stream I/O; exec may infer the sole retained session.",
	"handle":                "Exec task ID/name; never combine with session.",
	"instance_name":         "Display label for the current Agent instance; 1..64 printable Unicode characters, no surrounding whitespace. Omit session/settings.",
	"tool_profile":          "Discovery only: standard=18 daily tools; full=all 30. Omit session/settings. Sends tools/list_changed; no remote I/O.",
	"name":                  "Unique alias; sN/taskN reserved. Duplicates send nothing; stable ID remains valid.",
	"mode":                  "SSH defaults exec. Devices/console shells and Telnet require stream.",
	"ssh_jumps":             "Order: local -> ssh_jumps[0] -> ... -> target. SSH hops, default port 22; target may be SSH or Telnet.",
	"host_key":              "Pinned SHA256; overrides known_hosts; insecure=true overrides both.",
	"known_hosts":           "Explicit verification file; never auto-discovered or written.",
	"insecure":              "true overrides host_key/known_hosts; false alone does not enable verification.",
	"login":                 "Bash login shell; no fallback. false without cwd/env sends command unchanged.",
	"cwd":                   "Task-local directory after profiles/env; cd failure prevents command.",
	"env":                   "Task-local exports after profiles; string values, audited redacted.",
	"interactive":           "Retain stdin; otherwise EOF. Startup only.",
	"pty":                   "Requires interactive=true. Changes terminal line discipline/echo.",
	"collect_output":        "true collects output; false only sends/starts async task.",
	"line_end":              `Always append once, even if data ends with it. Default SSH \r, Telnet \r\n.`,
	"term_on_timeout":       "Completion timeout attempts TERM once; RPC/read timeouts never signal. Exit not guaranteed.",
	"exec_total_timeout_ms": "Completion wait, 1..300000ms; RPC budget <2min may return handle earlier.",
	"read_total_timeout_ms": "Read wait 1..110000ms; not used by exec/task_wait.",
	"end_pattern":           "Optional Go RE2 on original decoded text; disabled by default. Not an execution/login receipt.",
	"max_output_bytes":      "UTF-8 page budget, max 524288. History remains bounded.",
	"read_only":             "Connect/configure only; blocks application input and signals, not protocol negotiation.",
	"strip_ansi":            "Project output only; raw history/offsets preserved.",
	"include_pattern":       "RE2 line inclusion after ANSI projection; empty disables.",
	"exclude_pattern":       "RE2 line exclusion; empty disables. Filtered bytes advance cursor.",
	"data":                  "Text input; escaped tools decode one layer. No input/output attribution.",
}

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

func compactSchema(node any) {
	switch v := node.(type) {
	case map[string]any:
		delete(v, "description")
		if alternatives, ok := v["anyOf"].([]any); ok && len(alternatives) == 2 {
			a, aok := alternatives[0].(map[string]any)
			b, bok := alternatives[1].(map[string]any)
			if aok && bok && a["type"] == "integer" && b["type"] == "string" {
				delete(v, "anyOf")
				v["type"] = []string{"integer", "string"}
				v["maxLength"] = 128
			}
		}
		for _, child := range v {
			compactSchema(child)
		}
	case []any:
		for _, child := range v {
			compactSchema(child)
		}
	}
}

func buildPublicDefinitions(full map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(full))
	configKeys := core.ConfigDescriptions()
	b, _ := json.Marshal(core.DefaultConfig("ssh"))
	var defaults map[string]any
	_ = json.Unmarshal(b, &defaults)
	for name, original := range full {
		def := cloneMap(original)
		input := def["inputSchema"].(map[string]any)
		props := input["properties"].(map[string]any)
		for key := range props {
			if _, config := configKeys[key]; config && !applicableConfig(name, key) {
				delete(props, key)
			}
		}
		compactSchema(input)
		for key, field := range props {
			p := field.(map[string]any)
			if desc := briefParameters[key]; desc != "" {
				p["description"] = desc
			}
			if value, ok := defaults[key]; ok && key != "line_end" {
				p["default"] = value
			}
		}
		if data, ok := props["data"].(map[string]any); ok {
			data["description"] = "Literal text; no escape decoding or input/output attribution."
			if strings.HasSuffix(name, "_escaped") {
				data["description"] = `Decode once: visible \x03 sends Ctrl-C; \r \n \t \xNN \uNNNN supported. DSML string uses ONE backslash (JSON source doubles it). Invalid escapes send nothing.`
			}
		}
		// Reintroduce concise host verification hints inside each hop too.
		if jumps, ok := props["ssh_jumps"].(map[string]any); ok {
			jp := jumps["items"].(map[string]any)["properties"].(map[string]any)
			for _, key := range []string{"host_key", "known_hosts", "insecure"} {
				jp[key].(map[string]any)["description"] = briefParameters[key]
			}
		}
		if brief := briefTools[name]; brief != "" {
			def["description"] = brief
		}
		out[name] = def
	}
	return out
}
