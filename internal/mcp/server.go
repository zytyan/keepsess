// Package mcp implements the newline-delimited JSON-RPC stdio binding.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/zytyan/keepsess/internal/buildinfo"
	"github.com/zytyan/keepsess/internal/core"
)

const MaxInflight = 32
const WriteLimit = 3 * time.Second

type Audit interface{ Record(any) error }
type Server struct {
	Manager     *core.Manager
	Audit       Audit
	WebInfo     func() any
	ToolProfile string
	profileMu   sync.Mutex
	toolChanges chan struct{}
}
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func strict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func toolResult(v any, e error) any {
	bad := e != nil
	if e != nil {
		v = map[string]any{"error": e.Error()}
		var lookup *core.SessionLookupError
		if errors.As(e, &lookup) {
			v = core.Result{Session: lookup.Session, Error: e.Error(), DisconnectReason: lookup.Reason, NextAction: lookup.NextAction, InstanceID: lookup.InstanceID}
		}
		var taskLookup *core.TaskLookupError
		if errors.As(e, &taskLookup) {
			v = map[string]any{"error": e.Error(), "handle": taskLookup.Handle, "instance_id": taskLookup.InstanceID, "next_action": "sess_task_list"}
		}
		var route *core.RouteError
		if errors.As(e, &route) {
			v = map[string]any{"error": e.Error(), "hop_index": route.HopIndex, "host": route.Host, "port": route.Port, "from_host": route.FromHost, "stage": route.Stage, "target": route.Target}
		}
	}
	if r, ok := v.(map[string]any); ok && r["error"] != nil {
		bad = true
	}
	if r, ok := v.(core.Result); ok && r.Error != "" {
		bad = true
	}
	b, _ := json.Marshal(v)
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "structuredContent": v, "isError": bad}
}
func (s *Server) call(ctx context.Context, p json.RawMessage) any {
	var args struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      json.RawMessage `json:"_meta,omitempty"`
	}
	if e := strict(p, &args); e != nil {
		return toolResult(nil, e)
	}
	if len(args.Arguments) == 0 {
		args.Arguments = json.RawMessage(`{}`)
	}
	def, ok := definitions[args.Name]
	if !ok {
		return toolResult(nil, errors.New("unknown tool; refresh tools/list for the current API"))
	}
	var keys map[string]json.RawMessage
	if e := json.Unmarshal(args.Arguments, &keys); e != nil || keys == nil {
		return toolResult(nil, errors.New("arguments must be an object"))
	}
	props := def["inputSchema"].(map[string]any)["properties"].(map[string]any)
	for k := range keys {
		if _, ok := props[k]; !ok && !(k == "config" && props["encoding"] != nil) {
			return toolResult(nil, fmt.Errorf("unknown argument %q for %s; use sess_help(topic=%q) for parameters", k, args.Name, args.Name))
		}
	}
	for _, k := range def["inputSchema"].(map[string]any)["required"].([]string) {
		if _, ok := keys[k]; !ok {
			return toolResult(nil, fmt.Errorf("missing required argument %q", k))
		}
	}
	for _, key := range []string{"name", "instance_name", "tool_profile", "cwd", "login", "env"} {
		if raw, present := keys[key]; present {
			if string(raw) == "null" {
				return toolResult(nil, fmt.Errorf("%s cannot be null", key))
			}
			if key == "env" {
				var env map[string]json.RawMessage
				if err := json.Unmarshal(raw, &env); err != nil || env == nil {
					return toolResult(nil, errors.New("env must be an object of string values"))
				}
				for _, value := range env {
					var text string
					if string(value) == "null" {
						return toolResult(nil, errors.New("env values must be strings, not null"))
					}
					if err := json.Unmarshal(value, &text); err != nil {
						return toolResult(nil, errors.New("env values must be strings"))
					}
				}
			}
		}
	}
	if args.Name == "sess_exec" {
		if raw, present := keys["session"]; present {
			var id string
			if err := json.Unmarshal(raw, &id); err != nil || id == "" {
				return toolResult(nil, errors.New("session must be a non-empty string when supplied; omit it to infer the sole retained session"))
			}
		}
	}
	if strings.HasPrefix(args.Name, "sess_read_") {
		_, hasSession := keys["session"]
		_, hasHandle := keys["handle"]
		if hasSession == hasHandle {
			if hasSession {
				return toolResult(nil, errors.New("exec task reads require handle only: remove session; stream reads require session only: remove handle"))
			}
			return toolResult(nil, errors.New("read target missing: provide handle for an exec task or session for a stream"))
		}
	}
	if s.Audit != nil {
		var record any
		_ = json.Unmarshal(p, &record)
		if e := s.Audit.Record(record); e != nil {
			return toolResult(nil, fmt.Errorf("audit unavailable: %w", e))
		}
	}
	// Public tool arguments are flat. Preserve legacy nested config only at the
	// binding boundary; explicit top-level values win, without mutating defaults.
	if props["encoding"] != nil {
		patch := core.ConfigPatch{}
		if raw, ok := keys["config"]; ok {
			if err := json.Unmarshal(raw, &patch); err != nil || patch == nil {
				return toolResult(nil, errors.New("legacy config must be an object; supply configuration parameters at the top level"))
			}
		}
		for k := range core.ConfigDescriptions() {
			if raw, ok := keys[k]; ok {
				patch[k] = raw
				delete(keys, k)
			}
		}
		keys["config"], _ = json.Marshal(patch)
		args.Arguments, _ = json.Marshal(keys)
	}
	switch args.Name {
	case "sess_get_file", "sess_put_file":
		var r core.FileRequest
		if e := strict(args.Arguments, &r); e != nil {
			return toolResult(nil, e)
		}
		v, e := s.Manager.Transfer(ctx, args.Name == "sess_put_file", r)
		return toolResult(v, e)
	case "sess_tunnel_open", "sess_tunnel_list", "sess_tunnel_close":
		var r core.TunnelRequest
		if e := strict(args.Arguments, &r); e != nil {
			return toolResult(nil, e)
		}
		v, e := s.Manager.Tunnel(ctx, strings.TrimPrefix(args.Name, "sess_tunnel_"), r)
		return toolResult(v, e)

	case "sess_help":
		var r helpRequest
		if e := strict(args.Arguments, &r); e != nil {
			return toolResult(nil, e)
		}
		v, e := builtinHelp(r)
		return toolResult(v, e)
	case "sess_rename", "sess_task_rename":
		var r struct {
			Session string `json:"session"`
			Handle  string `json:"handle"`
			Name    string `json:"name"`
		}
		if e := strict(args.Arguments, &r); e != nil {
			return toolResult(nil, e)
		}
		if args.Name == "sess_rename" {
			v, e := s.Manager.RenameSession(r.Session, r.Name)
			return toolResult(v, e)
		}
		v, e := s.Manager.RenameTask(r.Handle, r.Name)
		return toolResult(v, e)
	case "sess_connect":
		var o core.Options
		if e := strict(args.Arguments, &o); e != nil {
			return toolResult(nil, e)
		}
		v, e := s.Manager.Connect(ctx, o)
		return toolResult(v, e)
	case "sess_configure":
		var r struct {
			Session      string           `json:"session"`
			InstanceName *string          `json:"instance_name"`
			ToolProfile  *string          `json:"tool_profile"`
			Config       core.ConfigPatch `json:"config"`
		}
		if e := strict(args.Arguments, &r); e != nil {
			return toolResult(nil, e)
		}
		if r.InstanceName != nil || r.ToolProfile != nil {
			if _, hasSession := keys["session"]; hasSession || len(r.Config) != 0 {
				return toolResult(nil, errors.New("instance-level instance_name/tool_profile cannot be mixed with session settings"))
			}
			if r.ToolProfile != nil && !ValidToolProfile(*r.ToolProfile) {
				return toolResult(nil, errors.New("tool_profile must be standard or full"))
			}
			if r.InstanceName != nil {
				if _, e := s.Manager.RenameInstance(*r.InstanceName); e != nil {
					return toolResult(nil, e)
				}
			}
			if r.ToolProfile != nil {
				_ = s.setToolProfile(*r.ToolProfile)
			}
			return toolResult(map[string]any{"instance_id": s.Manager.InstanceID(), "instance_name": s.Manager.InstanceName(), "tool_profile": s.toolProfile(), "tool_count": len(s.tools()), "comment": "Instance label/profile updated; no remote input. Tool profile changes notify the client to refresh tools/list."}, nil)
		}
		if r.Session == "" {
			return toolResult(nil, errors.New("provide instance_name/tool_profile without session, or session with session settings"))
		}
		if r.Config == nil {
			return toolResult(nil, errors.New("config must be an object"))
		}
		if len(r.Config) == 0 {
			v, e := s.Manager.GetConfig(r.Session)
			return toolResult(v, e)
		}
		v, e := s.Manager.Configure(r.Session, r.Config)
		return toolResult(v, e)
	case "sess_get_config", "sess_close", "sess_reconnect":
		var r struct {
			Session string `json:"session"`
		}
		if e := strict(args.Arguments, &r); e != nil {
			return toolResult(nil, e)
		}
		if args.Name == "sess_get_config" {
			v, e := s.Manager.GetConfig(r.Session)
			return toolResult(v, e)
		}
		if args.Name == "sess_reconnect" {
			v, e := s.Manager.Reconnect(ctx, r.Session)
			return toolResult(v, e)
		}
		v, e := s.Manager.Close(r.Session)
		return toolResult(v, e)
	case "sess_task_list":
		var r struct {
			Session string `json:"session,omitempty"`
		}
		if e := strict(args.Arguments, &r); e != nil {
			return toolResult(nil, e)
		}
		v, e := s.Manager.TaskList(r.Session)
		return toolResult(map[string]any{"instance_id": s.Manager.InstanceID(), "tasks": v}, e)
	case "sess_list":
		return toolResult(map[string]any{"instance_id": s.Manager.InstanceID(), "instance_name": s.Manager.InstanceName(), "sessions": s.Manager.List(), "web": s.webInfo()}, nil)
	case "sess_close_all":
		s.Manager.CloseAll()
		return toolResult(map[string]any{"reason": "closed", "connected": false}, nil)
	default:
		var r core.Request
		if e := strict(args.Arguments, &r); e != nil {
			return toolResult(nil, e)
		}
		v, e := s.Manager.Operate(ctx, strings.TrimPrefix(args.Name, "sess_"), r)
		return toolResult(v, e)
	}
}
func schema(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}
func str(desc string) any { return map[string]any{"type": "string", "description": desc} }
func integerSchema(desc string, min, max int64) any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max, "description": desc}
}
func configSchema(override bool) any {
	descriptions := core.ConfigDescriptions()
	props := map[string]any{}
	for k, desc := range descriptions {
		if override && (k == "connect_timeout_ms" || k == "session_idle_timeout_ms" || k == "tcp_keepalive_ms" || k == "ssh_keepalive_ms" || k == "ssh_keepalive_timeout_ms" || k == "read_only") {
			continue
		}
		if k == "read_only" || k == "term_on_timeout" || k == "strip_ansi" {
			props[k] = map[string]any{"type": "boolean", "description": desc, "default": k == "term_on_timeout"}
		} else if strings.HasSuffix(k, "_ms") || strings.HasSuffix(k, "_bytes") {
			props[k] = map[string]any{"description": desc + " Integer or arithmetic string, e.g. 30*60*1000. No eval/calls.", "anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string", "maxLength": 128}}}
		} else {
			props[k] = str(desc)
		}
	}
	return schema(props)
}

var definitions = buildDefinitions()

func buildDefinitions() map[string]map[string]any {
	defs := map[string]map[string]any{}
	add := func(name, desc string, p map[string]any, required ...string) {
		defs[name] = map[string]any{"name": name, "description": desc, "inputSchema": schema(p, required...)}
	}
	session := func() map[string]any {
		return map[string]any{"session": str("Stable session ID (s1) or current name alias. Always target an explicit session unless sess_exec can infer the sole retained session.")}
	}
	for _, name := range []string{"sess_get_file", "sess_put_file"} {
		p := session()
		p["src"] = str("Source file path; get: remote, put: local to keepsess process")
		p["dst"] = str("Destination file path; get may omit for an automatically created local temporary path; put requires remote path")
		p["backend"] = map[string]any{"type": "string", "enum": []string{"auto", "sftp", "scp", "exec"}, "default": "auto", "description": "Auto probes SFTP then SCP then raw SSH exec. Never retries a started transfer. No PTY or Bash login wrapper."}
		p["overwrite"] = map[string]any{"type": "boolean", "default": false, "description": "Permit destination overwrite. Uploads stage beside the destination; failures may leave a temporary_path. No automatic replay."}
		required := []string{"session", "src"}
		if name == "sess_put_file" {
			required = append(required, "dst")
		}
		add(name, "Transfer one regular file over existing SSH (exec or stream), including jumps. Local paths refer to keepsess host. 110s deadline; cancellation may close shared transport. Upload is read_only guarded; publishes by rename/link, requiring remote filesystem support. SFTP no-clobber requires hardlink extension; overwrite requires posix-rename. No recursive copy or automatic resume.", p, required...)
	}
	p := session()
	p["type"] = map[string]any{"type": "string", "enum": []string{"local", "remote", "dynamic", "remote_dynamic"}}
	p["bind_host"] = str("Listener host, default 127.0.0.1. Remote binding subject to SSH server GatewayPorts.")
	p["bind_port"] = integerSchema("Listener port, default 0 allocates a port", 0, 65535)
	p["target_host"] = str("Required for local/remote. Local targets connect from SSH server; remote targets from keepsess.")
	p["target_port"] = integerSchema("Required for local/remote", 1, 65535)
	add("sess_tunnel_open", "Open TCP forwarding or SOCKS5 CONNECT tunnel. Dynamic listens locally; remote_dynamic listens remotely. read_only blocks creation; existing tunnels remain. Disconnect/reconnect closes tunnels; never restored automatically. 16 listeners/session, 64 sockets/listener.", p, "session", "type")
	add("sess_tunnel_list", "List retained tunnel handles and bound addresses; closed transports require explicit close/reopen.", session(), "session")
	p = session()
	p["handle"] = str("Tunnel handle returned by open")
	add("sess_tunnel_close", "Close listener and all accepted connections, releasing the tunnel handle.", p, "session", "handle")
	connect := map[string]any{"protocol": map[string]any{"type": "string", "enum": []string{"ssh", "telnet"}}, "host": str("Hostname or IP; IPv6 without brackets"), "port": integerSchema("TCP port", 1, 65535), "username": str("SSH username"), "password": str("SSH password; authentication field redacted in audit"), "keyfile": str("Local SSH private key path"), "key_passphrase": str("SSH key passphrase; redacted in audit"), "known_hosts": str("Optional local known_hosts path enables verification; never automatically discovered or written. Used unless host_key or insecure=true takes precedence."), "host_key": str("Optional pinned SHA256 SSH host-key fingerprint enables verification; takes precedence over known_hosts unless insecure=true."), "insecure": map[string]any{"type": "boolean", "description": "Compatibility flag: true skips verification even when host_key/known_hosts is supplied. By default verification is skipped unless those fields are supplied; false alone does not enable it."}, "mode": map[string]any{"type": "string", "enum": []string{"stream", "exec"}, "description": "SSH defaults exec for independent commands; explicit exec interactive=true supports standalone interactive programs. Use stream for persistent terminal shells, console logins and network devices. Telnet always stream; never use sess_exec there."}, "config": configSchema(false), "name": str("Optional unique session name alias; stable sN ID remains valid. 1..64 letters/digits/._-, starts with alphanumeric; sN/taskN reserved.")}
	jump := map[string]any{}
	for _, name := range []string{"host", "port", "username", "password", "keyfile", "key_passphrase", "known_hosts", "host_key", "insecure"} {
		jump[name] = connect[name]
	}
	jump["port"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 65535, "default": 22}
	jumpSchema := schema(jump, "host", "username")
	jumpSchema["anyOf"] = []any{
		map[string]any{"required": []string{"password"}, "properties": map[string]any{"password": map[string]any{"minLength": 1}}},
		map[string]any{"required": []string{"keyfile"}, "properties": map[string]any{"keyfile": map[string]any{"minLength": 1}}},
	}
	connect["ssh_jumps"] = map[string]any{"type": "array", "maxItems": 8, "items": jumpSchema, "description": "Order example: local -> ssh_jumps[0] -> ssh_jumps[1] -> target. SSH forwarding chain for an SSH or Telnet target, first hop nearest this client, then subsequent hops, then the target host/port using protocol. Telnet targets require stream mode. Each hop authenticates and verifies its host key independently with the same precedence as the target. Port defaults 22. Entire chain shares connect_timeout_ms. No shell commands or automatic retries. Reconnect reuses the private chain."}
	add("sess_connect", "Create a persistent connection and short handle; no terminal output consumed. Optional ssh_jumps (old jumps rejected) are ordered SSH forwarding hops to either an SSH or Telnet target. protocol describes the target. Telnet requires stream; login input remains explicit via send/read. Telnet peer identity is not inferred from a jump. SSH exec has exit status; stream is bidirectional with no input/output attribution. Connect/reconnect return compact SSH peer server/key identity; no identity shell commands. No automatic reconnect/replay; sess_reconnect is explicit. Telnet is plaintext.", connect, "protocol", "host", "port")
	// SSH credentials are conditionally required; Telnet login uses stream input.
	// anyOf permits password and keyfile together, matching SSH authentication.
	defs["sess_connect"]["inputSchema"].(map[string]any)["allOf"] = []any{map[string]any{
		"if": map[string]any{"properties": map[string]any{"protocol": map[string]any{"const": "ssh"}}, "required": []string{"protocol"}},
		"then": map[string]any{
			"required":   []string{"username"},
			"properties": map[string]any{"username": map[string]any{"minLength": 1}},
			"anyOf": []any{
				map[string]any{"required": []string{"password"}, "properties": map[string]any{"password": map[string]any{"minLength": 1}}},
				map[string]any{"required": []string{"keyfile"}, "properties": map[string]any{"keyfile": map[string]any{"minLength": 1}}},
			},
		},
	}}
	add("sess_rename", "Rename a session alias without changing its stable ID, connection or running tasks. Names must be unique among retained/pending sessions. Old alias stops resolving; stable ID remains valid.", map[string]any{"session": str("Session ID or current name"), "name": str("New session name")}, "session", "name")
	add("sess_task_rename", "Rename a retained task alias; stable taskN ID remains valid. Task names are process-wide unique among retained records; no output consumed.", map[string]any{"handle": str("Task ID or current name"), "name": str("New task name")}, "handle", "name")
	add("sess_reconnect", "Explicitly replace an existing connection using its private original connection parameters and current config. Preserve handle; discard old output. No application input, login dialog or command replay. Discards retained task handles. SSH authentication runs normally. Busy while reading/writing/configuring or exec running. Failed reconnect retains parameters for explicit retry; close releases them.", session(), "session")
	p = session()
	p["config"] = configSchema(false)
	p["tool_profile"] = map[string]any{"type": "string", "enum": []string{"standard", "full"}, "description": "Instance-level discovery profile; omit session/settings. standard lists 18 daily tools; full lists all 30 (tunnels, escaped sendline, raw history and aliases). Emits tools/list_changed; existing calls stay compatible. ~/.keepsess.toml [tools].profile sets the startup default."}
	p["instance_name"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "description": "Instance label: 1..64 printable Unicode characters, no surrounding whitespace. Supply alone without session settings; no remote I/O. Shown in the shared Web interface and archives; instance_id stays unchanged."}
	add("sess_configure", "Without session: set instance_name (display label) and/or tool_profile (standard/full discovery). With session alone: inspect effective config. With session and patches: atomically update defaults; omitted fields unchanged. Cannot mix instance and session changes. Current readers/writers retain snapshots; no authentication data returned.", p)
	defs["sess_configure"]["inputSchema"].(map[string]any)["oneOf"] = []any{
		map[string]any{"anyOf": []any{map[string]any{"required": []string{"instance_name"}}, map[string]any{"required": []string{"tool_profile"}}}, "not": map[string]any{"required": []string{"session"}}},
		map[string]any{"required": []string{"session"}, "not": map[string]any{"anyOf": []any{map[string]any{"required": []string{"instance_name"}}, map[string]any{"required": []string{"tool_profile"}}}}},
	}
	add("sess_get_config", "All effective settings with units, descriptions and actual platform TCP queue capability; no credentials.", session(), "session")
	add("sess_list", "List process-local sessions and transport/exec state.", map[string]any{})
	add("sess_close", "Discard this connection, all task handles and all buffers. Remote programs are not guaranteed to terminate. No terminal output returned; subsequent reads fail. Does not wait for readers/writers.", session(), "session")
	add("sess_close_all", "Discard every connection and buffer. No terminal output returned.", map[string]any{})
	add("sess_task_close_stdin", "Send SSH stdin EOF to an interactive exec task, leaving stdout/stderr and exit status readable. This is not a Ctrl-D byte or termination signal. read_only blocks it. Idempotent once closed; no output consumed. Bounded by write_timeout_ms; stalled/cancelled writes close shared transport and leave outcomes unknown.", map[string]any{"handle": str("Interactive exec task handle")}, "handle")
	add("sess_task_list", "List retained exec tasks, optionally filtered by session. Does not consume output. Handles are process-local task1, task2, ... and never reused. Finished records expire after 30 minutes and may be evicted earlier at retention limits.", session())
	for _, kind := range []string{"status", "wait", "signal", "release"} {
		p := map[string]any{"handle": str("Exec task handle, e.g. task22. Not a connection session ID.")}
		required := []string{"handle"}
		desc := ""
		switch kind {
		case "status":
			desc = "Immediately inspect task state, exit_code/exit_signal/warning, unread output (more), interactive/pty/stdin_open, and latest signal request. Does not consume output. Missing exit status is unknown outcome even if state=finished."
		case "wait":
			p["config"] = configSchema(true)
			desc = "Wait for this task's completion and consume incremental output. Uses top-level exec_total_timeout_ms and term_on_timeout (override session defaults and legacy config values), ignoring read timeouts/patterns. Does not launch/replay input or occupy the synchronous exec slot. Configured completion timeout attempts TERM by default and retains handle; RPC budget expiry does not signal. Cancellation/output_limit do not signal. One reader per task."
		case "signal":
			p["signal"] = map[string]any{"type": "string", "enum": []string{"ABRT", "ALRM", "FPE", "HUP", "ILL", "INT", "KILL", "PIPE", "QUIT", "SEGV", "TERM", "USR1", "USR2"}, "description": "SSH signal name without SIG prefix. sent confirms only the request write, not delivery or program exit. read_only blocks signals."}
			required = append(required, "signal")
			desc = "Send one bounded SSH signal request to a running task, independent of output collection. Does not guarantee exit or terminate a process tree. No automatic escalation/retry. Stalled transport writes close the shared connection and report unknown outcomes."
		case "release":
			desc = "Delete a finished task record and retained output. Running/unknown tasks and active operations return busy; use signal to request termination and inspect completion first. Connection close discards all associated handles without guaranteeing remote exit."
		}
		add("sess_task_"+kind, desc, p, required...)
	}
	for _, kind := range []string{"exec", "send_text", "send_escaped", "sendline_text", "sendline_escaped", "read_available", "read_until", "read_tail", "read_history"} {
		p := session()
		p["config"] = configSchema(true)
		desc := ""
		required := []string{"session"}
		switch kind {
		case "exec":
			p["name"] = str("Optional unique task name alias, reserved before any exec request; duplicates send nothing. Stable taskN ID remains valid.")
			p["login"] = map[string]any{"type": "boolean", "default": true, "description": "Use bash --login -c by default; noninteractive, no implicit PTY/stdin. Missing Bash fails without fallback/retry. false sends the original command if cwd/env omitted; otherwise uses bash -c."}
			p["cwd"] = str("Task-local working directory, applied after login initialization and env; failure prevents command execution.")
			p["env"] = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "maxProperties": 128, "description": "Task-local exported environment after login profiles; names must be shell identifiers. Values are audited as redacted; no session state changes."}
			required = []string{"command"}
			p["session"] = map[string]any{"type": "string", "minLength": 1, "description": "Optional only when exactly one session is retained (including disconnected/stream sessions). Zero sessions requires connect; multiple sessions require an explicit session. Never selects by mode, connectivity or recency."}
			p["command"] = str("One SSH exec command; shell state does not persist across commands.")
			p["interactive"] = map[string]any{"type": "boolean", "default": false, "description": "Explicitly retain task stdin before startup; send tools can then target its handle. Default false delivers stdin EOF. Cannot enable after startup. Does not change collection or timeout defaults."}
			p["pty"] = map[string]any{"type": "boolean", "default": false, "description": "Request vt100 PTY before exec startup; requires interactive=true. May change echo, line endings and stdout/stderr separation."}
			desc = "Noninteractive Bash login shell by default (login=true); missing Bash fails without fallback. cwd/env override profiles for this task only. Session may be omitted only when exactly one session is retained; disconnected sessions require explicit reconnect. Results identify the actual session. SSH mode=exec only; default stdin EOF. Explicit interactive=true retains stdin, optionally pty=true for terminal programs such as GDB. Use handle-based send/read, and task_close_stdin for EOF. Persistent stream shells/console logins and all Telnet still use stream send/read; exec rejects those connections. SSH/Telnet stream sessions are rejected immediately without sending input. Top-level exec_total_timeout_ms and term_on_timeout override session defaults and legacy config values. Wait for protocol completion using independent exec_total_timeout_ms (default 120000ms, maximum 300000ms). Ignores read_total_timeout_ms, read_idle_timeout_ms and end_pattern. Output limits, cancellation and transport errors can return early. term_on_timeout defaults true: on completion wait timeout attempt TERM once, then return handle regardless of whether the program exited. Cancellation/output limits do not signal. All early returns release the synchronous slot. collect_output=false returns a task handle without a runtime timer and permits parallel commands; synchronous calls exclude only other synchronous calls on this session (busy/not_sent, no queue). Sync completion with all output returned omits handle; history remains in task_list. There is no exec silence watchdog. Missing exit status means unknown outcome."
		case "send_text", "sendline_text":
			p["data"] = str("Literal text encoded with session encoding; no escape decoding. sendline appends configured line_end exactly once, even if data already ends with it. Empty data allowed.")
			required = append(required, "data")
			desc = "Send encoded text and collect unread output by default; collect_output=false only sends. Stream session or interactive exec task handle. Does not decode escapes; sendline appends line_end. Collection is not attributed to this input. sent/written_bytes acknowledge LOCAL write completion only, not peer receipt, password acceptance or shell readiness. Empty output/start=end leaves authentication unknown; do not resend credentials or add Enter. Use a bounded read_until with observed prompts."
		case "send_escaped", "sendline_escaped":
			p["data"] = str(`Visible escape text decoded exactly once AFTER JSON decoding. Logical data value \x03 (one visible backslash) sends Ctrl-C; as-is DSML string parameters use that logical value, not JSON-doubled backslashes. \0 \r \n \t and \xNN are bytes; \uNNNN/\UNNNNNNNN and ordinary Unicode use configured encoding. JSON {"data":"\\x03"} sends Ctrl-C. \\ sends literal backslash. Invalid escapes reject before writing. sendline appends line_end.`)
			required = append(required, "data")
			desc = "Decode one escape layer, send and collect unread output by default; collect_output=false only sends. Stream session or interactive exec task handle. Explicit escape-to-byte input for XML/JSON agents; no input IDs or automatic replay. sendline appends configured ending exactly once. sent/written_bytes mean LOCAL write completion, not peer delivery/authentication. Empty output is inconclusive; never replay credentials or add Enter on silence."
		case "read_available":
			desc = "Immediately consume currently available unread output, bounded by max_output_bytes; never wait. A partial encoded character remains unread until completed."
		case "read_until":
			desc = "Consume unread and future output until match, total_timeout, idle_timeout, aborted, cancelled, output_limit, closed or SSH exec_finish. Default total 30000ms, idle 500ms; timeouts preserve connection. Pattern match consumes only through match."
		case "read_tail":
			p["lines"] = integerSchema("Last N retained lines; default 20; does not move incremental cursor.", 1, 100000)
			desc = "Read last N retained lines immediately, bounded by output budget. Raw offsets allow paging with read_history; no cursor change."
		case "read_history":
			p["offset"] = integerSchema("Absolute raw byte offset; default retained start. Use returned end to page; does not move incremental cursor.", 0, 1<<53)
			desc = "Read retained history immediately, bounded by output budget, not future output. Dropped bytes are reported. Page with offset=end."
		}
		if kind == "exec" || strings.HasPrefix(kind, "send") {
			collection := "Default true: after writing collect with read_until. False returns only a send receipt. Per-call settings never change session defaults. strip_ansi/include_pattern/exclude_pattern project output without altering raw history; scanning is bounded at 64KiB and may return scan_limit."
			if kind == "exec" {
				collection = "Default true: wait for SSH completion with exec_total_timeout_ms, independent of read settings/patterns. False returns a task handle and startup receipt; no execution wait or background timeout. Per-call config never changes session settings."
			}
			p["collect_output"] = map[string]any{"type": "boolean", "default": true, "description": collection}
		}
		if strings.HasPrefix(kind, "read_") || strings.HasPrefix(kind, "send") {
			p["handle"] = str("Exec task handle; send requires a task started with interactive=true. Provide exactly one of handle or stream session.")
			required = nil
			if strings.HasPrefix(kind, "send") {
				required = []string{"data"}
			}
			desc += " Exec tasks require handle only (delete session); streams require session only (delete handle). Output projection options preserve raw history/offsets, scan at most 64KiB/page, and report scan_limit/view_incomplete plus filter counters. Ordinary read/send collection timeouts never send TERM. Empty output includes an explanatory comment; it is not evidence of execution/authentication success."
		}
		add("sess_"+kind, desc, p, required...)
		if strings.HasPrefix(kind, "read_") || strings.HasPrefix(kind, "send") {
			defs["sess_"+kind]["inputSchema"].(map[string]any)["oneOf"] = []any{map[string]any{"required": []string{"session"}}, map[string]any{"required": []string{"handle"}}}
		}
	}
	defs["sess_help"] = helpToolSchema()
	// Present one flat parameter namespace to MCP clients for every tool.
	for _, def := range defs {
		input := def["inputSchema"].(map[string]any)
		props := input["properties"].(map[string]any)
		if nested, ok := props["config"]; ok {
			for key, field := range nested.(map[string]any)["properties"].(map[string]any) {
				props[key] = field
			}
			delete(props, "config")
			required := []string{}
			for _, key := range input["required"].([]string) {
				if key != "config" {
					required = append(required, key)
				}
			}
			input["required"] = required
		}
	}
	return defs
}
func Tools() []any {
	out := make([]any, 0, len(toolOrder))
	for _, name := range toolOrder {
		out = append(out, publicDefinitions[name])
	}
	return out
}

// Serve never executes remote operations on its input dispatcher. One bounded
// output writer owns stdout; a blocked consumer ends Serve within WriteLimit.
// A stream cannot deliver past a non-reading peer, so the process must exit.
func (s *Server) Serve(parent context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	changes := make(chan struct{}, 1)
	s.profileMu.Lock()
	s.toolChanges = changes
	s.profileMu.Unlock()
	defer func() { s.profileMu.Lock(); s.toolChanges = nil; s.profileMu.Unlock() }()
	defer s.Manager.CloseAll()
	type line struct {
		b   []byte
		err error
	}
	incoming := make(chan line, 1)
	go func() {
		scan := bufio.NewScanner(in)
		// data_escaped can encode one byte as \U00000041 (ten characters),
		// plus JSON escaping. Bound the frame with room for the RPC envelope.
		scan.Buffer(make([]byte, 4096), 12*core.MaxInputLimit+(128<<10))
		for scan.Scan() {
			b := append([]byte(nil), scan.Bytes()...)
			select {
			case incoming <- line{b: b}:
			case <-ctx.Done():
				return
			}
		}
		e := scan.Err()
		if e == nil {
			e = io.EOF
		}
		select {
		case incoming <- line{err: e}:
		case <-ctx.Done():
		}
	}()
	type output struct {
		b   []byte
		ack chan struct{}
	}
	queue := make(chan output, 64)
	writerErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case item := <-queue:
				if item.ack != nil {
					close(item.ack)
					continue
				}
				b := item.b
				done := make(chan error, 1)
				go func() {
					for len(b) > 0 {
						n, e := out.Write(b)
						if e != nil {
							done <- e
							return
						}
						if n == 0 {
							done <- io.ErrShortWrite
							return
						}
						b = b[n:]
					}
					done <- nil
				}()
				timer := time.NewTimer(WriteLimit)
				select {
				case e := <-done:
					timer.Stop()
					if e != nil {
						writerErr <- e
						return
					}
				case <-timer.C:
					writerErr <- errors.New("stdio output blocked for 3s; terminating instead of blocking request dispatcher")
					return
				case <-ctx.Done():
					timer.Stop()
					return
				}
			}
		}
	}()
	send := func(id json.RawMessage, result any, code int, msg string) {
		v := map[string]any{"jsonrpc": "2.0", "id": id}
		if code != 0 {
			v["error"] = map[string]any{"code": code, "message": msg}
		} else {
			v["result"] = result
		}
		b, e := json.Marshal(v)
		if e != nil {
			cancel()
			return
		}
		b = append(b, '\n')
		select {
		case queue <- output{b: b}:
		default:
			cancel()
		}
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, MaxInflight)
	var mu sync.Mutex
	active := make(map[string]context.CancelFunc)
	var eofDone chan struct{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-writerErr:
			return e
		case <-changes:
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
			b = append(b, '\n')
			select {
			case queue <- output{b: b}:
			default:
				cancel()
			}
		case <-eofDone:
			ack := make(chan struct{})
			timer := time.NewTimer(WriteLimit + time.Second)
			select {
			case queue <- output{ack: ack}:
			case e := <-writerErr:
				timer.Stop()
				return e
			case <-timer.C:
				return errors.New("stdio drain deadline")
			}
			select {
			case <-ack:
				timer.Stop()
				return nil
			case e := <-writerErr:
				timer.Stop()
				return e
			case <-timer.C:
				return errors.New("stdio drain deadline")
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}

		case l := <-incoming:
			if l.err != nil {
				if l.err != io.EOF {
					return l.err
				}
				incoming = nil
				eofDone = make(chan struct{})
				go func() { wg.Wait(); close(eofDone) }()
				continue
			}
			var m message
			if e := json.Unmarshal(l.b, &m); e != nil {
				send(json.RawMessage("null"), nil, -32700, "parse error")
				continue
			}
			if m.JSONRPC != "2.0" || m.Method == "" {
				send(json.RawMessage("null"), nil, -32600, "invalid request")
				continue
			}
			if len(m.ID) == 0 {
				if m.Method == "notifications/cancelled" {
					var p struct {
						RequestID json.RawMessage `json:"requestId"`
					}
					if json.Unmarshal(m.Params, &p) == nil {
						mu.Lock()
						c := active[string(p.RequestID)]
						mu.Unlock()
						if c != nil {
							c()
						}
					}
				}
				continue
			}
			var id any
			if json.Unmarshal(m.ID, &id) != nil {
				send(json.RawMessage("null"), nil, -32600, "invalid id")
				continue
			}
			switch id.(type) {
			case string, float64:
			default:
				send(json.RawMessage("null"), nil, -32600, "id must be string or number")
				continue
			}
			switch m.Method {
			case "initialize":
				var p struct {
					ProtocolVersion string `json:"protocolVersion"`
				}
				_ = json.Unmarshal(m.Params, &p)
				version := p.ProtocolVersion
				switch version {
				case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
				default:
					version = "2025-11-25"
				}
				send(m.ID, map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{"listChanged": true}}, "serverInfo": map[string]any{"name": "keepsess", "version": buildinfo.String()}, "_meta": map[string]any{"instance_id": s.Manager.InstanceID(), "web": s.webInfo()}, "instructions": residentInstructions}, 0, "")
			case "ping":
				send(m.ID, map[string]any{}, 0, "")
			case "tools/list":
				send(m.ID, map[string]any{"tools": s.tools()}, 0, "")
			case "tools/call":
				mu.Lock()
				_, dup := active[string(m.ID)]
				mu.Unlock()
				if dup {
					send(m.ID, nil, -32600, "duplicate active request id")
					continue
				}
				select {
				case slots <- struct{}{}:
				default:
					send(m.ID, toolResult(nil, errors.New("server busy: maximum 32 concurrent calls; reduce request rate")), 0, "")
					continue
				}
				workCtx, stop := context.WithTimeout(ctx, core.CallLimit)
				workCtx = core.WithRPCBudget(workCtx)
				mu.Lock()
				active[string(m.ID)] = stop
				mu.Unlock()
				wg.Add(1)
				go func(m message, workCtx context.Context, stop context.CancelFunc) {
					defer wg.Done()
					defer func() { stop(); mu.Lock(); delete(active, string(m.ID)); mu.Unlock() }()
					result := make(chan any, 1)
					// A stuck local filesystem/library operation cannot hold up its RPC.
					// Its worker slot remains reserved until it actually finishes.
					go func() { defer func() { <-slots }(); result <- s.call(workCtx, m.Params) }()
					select {
					case v := <-result:
						send(m.ID, v, 0, "")
					case <-workCtx.Done():
						select {
						case v := <-result:
							send(m.ID, v, 0, "")
						case <-time.After(50 * time.Millisecond):
							send(m.ID, toolResult(nil, fmt.Errorf("call cancelled or hard deadline reached: %w; remote command may still be running; inspect session before retrying", workCtx.Err())), 0, "")
						}
					}
				}(m, workCtx, stop)

			default:
				send(m.ID, nil, -32601, "method not found")
			}
		}
	}
}

func (s *Server) webInfo() any {
	if s.WebInfo != nil {
		return s.WebInfo()
	}
	return map[string]any{"enabled": false}
}
