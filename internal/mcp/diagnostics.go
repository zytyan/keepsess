package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zytyan/keepsess/internal/core"
)

// APIContract identifies the tool surface, independently of the binary build.
// Bump when tool names, arguments or their execution semantics change.
const APIContract = "2"

// These are corrections, not accepted aliases. Never reinterpret units or send
// a modified request automatically. Only suggest fields supported by this tool.
var parameterCorrections = map[string]string{
	"exec_timeout_ms": "timeout_ms", "exec_timeout": "timeout_ms",
	"execution_timeout": "timeout_ms",
	"max_output_tokens": "max_output_bytes", "max_output": "max_output_bytes",
	"task": "handle", "task_id": "handle", "session_id": "session",
	"jumps": "ssh_jumps", "text": "data", "cmd": "command",
}

func argumentError(tool, key string, missing bool) error {
	kind := "unknown"
	if missing {
		kind = "missing required"
	}
	message := fmt.Sprintf("%s argument %q for %s", kind, key, tool)
	props := helpProperties(tool)
	candidate := parameterCorrections[key]
	if key == "timeout" || key == "timeout_ms" {
		if tool == "exec" || tool == "task_wait" {
			candidate = "timeout_ms"
		} else if tool == "read_until" || strings.HasPrefix(tool, "send") {
			candidate = "read_total_timeout_ms"
		}
	}
	if field, ok := props[preferredParameter(tool, candidate)].(map[string]any); ok && !missing {
		message += fmt.Sprintf("; use %q: %s", candidate, field["description"])
		if key == "max_output_tokens" {
			message += " Bytes are not tokens; choose a byte limit explicitly."
		}
	}
	example := minimalExample(tool)
	b, _ := json.Marshal(example)
	return fmt.Errorf("%s; example: %s(%s). No application input was sent; no operation was performed. Parameters are top-level; session/handle each accepts its stable ID or current name", message, tool, b)
}

// Examples use placeholders only, never values supplied in a failed request.
func minimalExample(tool string) map[string]any {
	out := make(map[string]any)
	required := definitions[tool]["inputSchema"].(map[string]any)["required"].([]string)
	for _, key := range required {
		value := any("...")
		switch key {
		case "session":
			value = "s1"
		case "handle":
			value = "task1"
			if strings.HasPrefix(tool, "tunnel_") {
				value = "tunnel1"
			}
		case "name":
			value = "build"
		case "signal":
			value = "TERM"
		case "command":
			value = "pwd"
		case "data":
			value = "show version"
		case "type":
			value = "dynamic"
		}
		out[key] = value
	}
	switch {
	case tool == "connect":
		return map[string]any{"protocol": "ssh", "host": "server.example", "port": 22, "username": "user", "keyfile": "/path/to/key", "mode": "exec"}
	case tool == "exec":
		out["command"] = "pwd"
		out["session"] = "s1"
		c := core.DefaultConfig("ssh")
		out["timeout_ms"] = c.ExecTotalTimeoutMS
		out["max_output_bytes"] = c.MaxOutputBytes
		out["term_on_timeout"] = c.TermOnTimeout
	case strings.HasPrefix(tool, "send"):
		out["session"] = "s1"
		out["data"] = "show version"
	case strings.HasPrefix(tool, "read_"):
		out["handle"] = "task1"
	case tool == "configure":
		out["session"] = "s1"
	}
	return out
}

func unknownTool(name string) error {
	candidate := strings.TrimPrefix(strings.TrimPrefix(name, "keepsess_"), "sess_")
	if _, ok := definitions[candidate]; ok {
		return fmt.Errorf("unknown tool %q; server tool name is %q (client may add keepsess_). Refresh tools/list; no operation was performed", name, candidate)
	}
	return fmt.Errorf("unknown tool %q; refresh tools/list for current names (connect, exec, sendline_text, task_signal, ...). No operation was performed", name)
}

func explainResult(v core.Result, tool string, keys map[string]json.RawMessage) core.Result {
	target := map[string]any{}
	if v.Handle != "" {
		target["handle"] = v.Handle
	} else if v.Session != "" {
		target["session"] = v.Session
	}
	// Preserve transport/reconnect guidance; never propose another command when
	// its original request may have started.
	if v.NextAction != "" {
		if v.DroppedBytes > 0 {
			v.Guidance += " Some raw history was dropped; further reads cannot recover that gap."
		}
		return v
	}
	switch {
	case strings.Contains(v.Error, "synchronous exec call in progress"):
		v.NextAction, v.Guidance = "task_list", "Nothing sent. Wait for the active synchronous call to return; inspect tasks if needed. Do not poll exec in a busy loop."
		if v.Session != "" {
			v.NextArguments = map[string]any{"session": v.Session}
		}
	case strings.Contains(v.Error, "resource_limit:"):
		v.NextAction, v.Guidance = "task_list", "Nothing sent. Inspect retained tasks; wait for running tasks or release completed, inactive tasks. Do not repeatedly submit exec."
	case strings.Contains(v.Error, "busy"):
		if v.Handle != "" {
			v.NextAction, v.NextArguments = "task_status", target
		} else {
			v.NextAction = "list"
		}
		v.Guidance = "This operation did not acquire its required lock. Let the active operation return before trying again; do not poll in a busy loop."
	case v.Reason == "output_limit" || v.Reason == "scan_limit":
		v.NextAction, v.NextArguments = "read_available", target
		if tool == "read_history" || tool == "read_tail" {
			v.NextAction = "read_history"
			v.NextArguments["offset"] = v.End
		}
		var legacy map[string]json.RawMessage
		_ = json.Unmarshal(keys["config"], &legacy)
		for _, key := range []string{"strip_ansi", "include_pattern", "exclude_pattern", "encoding", "output_decode_error_action", "max_output_bytes"} {
			raw := keys[key]
			if raw == nil {
				raw = legacy[key]
			}
			if raw != nil {
				var value any
				if json.Unmarshal(raw, &value) == nil {
					v.NextArguments[preferredParameter(v.NextAction, key)] = value
				}
			}
		}
		v.Guidance = "Continue reading the same reference; do not execute or send the input again. start/end are raw byte offsets; more reports unread retained bytes."
		if v.MinimumOutputBytes > 0 {
			v.NextArguments["max_output_bytes"] = v.MinimumOutputBytes
			v.Guidance += " Increase max_output_bytes to at least minimum_output_bytes for the next indivisible character/fragment."
		}
	}
	if v.DroppedBytes > 0 {
		v.Guidance += " Some raw history was dropped; further reads cannot recover that gap."
	}
	return v
}
