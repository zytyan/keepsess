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
	Manager *core.Manager
	Audit   Audit
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
		return toolResult(nil, errors.New("unknown tool; refresh tools/list for the 0.3 API"))
	}
	var keys map[string]json.RawMessage
	if e := json.Unmarshal(args.Arguments, &keys); e != nil || keys == nil {
		return toolResult(nil, errors.New("arguments must be an object"))
	}
	props := def["inputSchema"].(map[string]any)["properties"].(map[string]any)
	for k := range keys {
		if _, ok := props[k]; !ok {
			return toolResult(nil, fmt.Errorf("unknown argument %q for %s", k, args.Name))
		}
	}
	for _, k := range def["inputSchema"].(map[string]any)["required"].([]string) {
		if _, ok := keys[k]; !ok {
			return toolResult(nil, fmt.Errorf("missing required argument %q", k))
		}
	}
	if s.Audit != nil {
		var record any
		_ = json.Unmarshal(p, &record)
		if e := s.Audit.Record(record); e != nil {
			return toolResult(nil, fmt.Errorf("audit unavailable: %w", e))
		}
	}
	switch args.Name {
	case "sess_connect":
		var o core.Options
		if e := strict(args.Arguments, &o); e != nil {
			return toolResult(nil, e)
		}
		v, e := s.Manager.Connect(ctx, o)
		return toolResult(v, e)
	case "sess_configure":
		var r struct {
			Session string           `json:"session"`
			Config  core.ConfigPatch `json:"config"`
		}
		if e := strict(args.Arguments, &r); e != nil {
			return toolResult(nil, e)
		}
		if r.Config == nil {
			return toolResult(nil, errors.New("config must be an object"))
		}
		v, e := s.Manager.Configure(r.Session, r.Config)
		return toolResult(v, e)
	case "sess_get_config", "sess_close":
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
		v, e := s.Manager.Close(r.Session)
		return toolResult(v, e)
	case "sess_list":
		return toolResult(map[string]any{"sessions": s.Manager.List()}, nil)
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
		if override && (k == "connect_timeout_ms" || k == "session_idle_timeout_ms" || k == "tcp_keepalive_ms") {
			continue
		}
		if strings.HasSuffix(k, "_ms") || strings.HasSuffix(k, "_bytes") {
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
		return map[string]any{"session": str("Short handle returned by sess_connect, e.g. s1. Always target an explicit session.")}
	}
	connect := map[string]any{"protocol": map[string]any{"type": "string", "enum": []string{"ssh", "telnet"}}, "host": str("Hostname or IP; IPv6 without brackets"), "port": integerSchema("TCP port", 1, 65535), "username": str("SSH username"), "password": str("SSH password; authentication field redacted in audit"), "keyfile": str("Local SSH private key path"), "key_passphrase": str("SSH key passphrase; redacted in audit"), "known_hosts": str("Local known_hosts path"), "host_key": str("Pinned SHA256 SSH host-key fingerprint"), "insecure": map[string]any{"type": "boolean", "description": "Explicitly skip SSH host verification"}, "mode": map[string]any{"type": "string", "enum": []string{"stream", "exec"}, "description": "SSH defaults exec; choose stream for device consoles. Telnet always stream."}, "config": configSchema(false)}
	add("sess_connect", "Create a persistent connection and short handle; no terminal output consumed. SSH exec has exit status; stream is bidirectional with no input/output attribution. No reconnect/replay. Telnet is plaintext.", connect, "protocol", "host", "port")
	p := session()
	p["config"] = configSchema(false)
	add("sess_configure", "Atomically patch session settings; omitted fields unchanged. Current readers/writers retain snapshots. No authentication data in returned config.", p, "session", "config")
	add("sess_get_config", "All effective settings with units, descriptions and actual platform TCP queue capability; no credentials.", session(), "session")
	add("sess_list", "List process-local sessions and transport/exec state.", map[string]any{})
	add("sess_close", "Discard this connection and all buffers. No terminal output returned; subsequent reads fail. Does not wait for readers/writers.", session(), "session")
	add("sess_close_all", "Discard every connection and buffer. No terminal output returned.", map[string]any{})
	for _, kind := range []string{"exec", "send_raw", "send_escaped", "sendline_raw", "sendline_escaped", "read_available", "read_until", "read_tail", "read_all"} {
		p := session()
		p["config"] = configSchema(true)
		desc := ""
		required := []string{"session"}
		switch kind {
		case "exec":
			p["command"] = str("One SSH exec command; shell state does not persist across commands.")
			required = append(required, "command")
			desc = "SSH mode=exec only. Wait for protocol completion using independent exec_total_timeout_ms (default 30000ms). Ignores read_total_timeout_ms, read_idle_timeout_ms and end_pattern. Output limits, cancellation and transport errors can return early. Timeout does not stop the command: inspect running and read without replaying. Missing exit status means unknown outcome."
		case "send_raw", "sendline_raw":
			p["data"] = str("Literal text encoded with session encoding; no escape decoding. sendline appends configured line_end exactly once, even if data already ends with it. Empty data allowed.")
			required = append(required, "data")
			desc = "Stream only. Encode text and write with bounded deadline; sendline appends line_end. Optional collection observes the unread stream, not output attributed to this input. Never replay sent/unknown input on empty output."
		case "send_escaped", "sendline_escaped":
			p["data"] = str(`Visible escape text decoded exactly once AFTER JSON decoding. \0 \r \n \t and \xNN are bytes; \uNNNN/\UNNNNNNNN and ordinary Unicode use configured encoding. JSON {"data":"\\x03"} sends Ctrl-C. \\ sends literal backslash. Invalid escapes reject before writing. sendline appends line_end.`)
			required = append(required, "data")
			desc = "Stream only. Explicit escape-to-byte input for XML/JSON agents; no input IDs or automatic replay. sendline appends configured ending exactly once."
		case "read_available":
			desc = "Immediately consume currently available unread output, bounded by max_output_bytes; never wait. A partial encoded character remains unread until completed."
		case "read_until":
			desc = "Consume unread and future output until match, total_timeout, idle_timeout, aborted, cancelled, output_limit, closed or SSH exec_finish. Default total 30000ms, idle 500ms; timeouts preserve connection. Pattern match consumes only through match."
		case "read_tail":
			p["lines"] = integerSchema("Last N retained lines; default 20; does not move incremental cursor.", 1, 100000)
			desc = "Read last N retained lines immediately, bounded by output budget. Raw offsets allow paging with read_all; no cursor change."
		case "read_all":
			p["offset"] = integerSchema("Absolute raw byte offset; default retained start. Use returned end to page; does not move incremental cursor.", 0, 1<<53)
			desc = "Read retained history immediately, bounded by output budget, not future output. Dropped bytes are reported. Page with offset=end."
		}
		if kind == "exec" || strings.HasPrefix(kind, "send") {
			collection := "Default true: after writing collect with read_until. False returns only a send receipt. Per-call config never changes session settings."
			if kind == "exec" {
				collection = "Default true: wait for SSH completion with exec_total_timeout_ms, independent of read settings/patterns. False returns only a send receipt. Per-call config never changes session settings."
			}
			p["collect_output"] = map[string]any{"type": "boolean", "default": true, "description": collection}
		}
		add("sess_"+kind, desc, p, required...)
	}
	return defs
}
func Tools() []any {
	order := []string{"sess_connect", "sess_configure", "sess_get_config", "sess_list", "sess_exec", "sess_send_raw", "sess_send_escaped", "sess_sendline_raw", "sess_sendline_escaped", "sess_read_available", "sess_read_until", "sess_read_tail", "sess_read_all", "sess_close", "sess_close_all"}
	out := make([]any, 0, len(order))
	for _, name := range order {
		out = append(out, definitions[name])
	}
	return out
}

// Serve never executes remote operations on its input dispatcher. One bounded
// output writer owns stdout; a blocked consumer ends Serve within WriteLimit.
// A stream cannot deliver past a non-reading peer, so the process must exit.
func (s *Server) Serve(parent context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
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
				send(m.ID, map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "keepsess", "version": buildinfo.String()}, "instructions": "Use SSH mode=exec for commands; mode=stream for consoles. Four send tools optionally collect unread stream output. No input/output attribution or input IDs. Defaults: read total 30000ms, idle 500ms, output 8192 bytes. sess_exec uses independent exec_total_timeout_ms (30000ms), ignoring read timeouts and end_pattern. Configure session or override config per operation. Never replay sent/unknown inputs. close discards buffers."}, 0, "")
			case "ping":
				send(m.ID, map[string]any{}, 0, "")
			case "tools/list":
				send(m.ID, map[string]any{"tools": Tools()}, 0, "")
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
