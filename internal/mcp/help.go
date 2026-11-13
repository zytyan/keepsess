package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zytyan/keepsess/internal/core"
)

type helpRequest struct {
	Topic     string `json:"topic"`
	Parameter string `json:"parameter"`
	Query     string `json:"query"`
	Limit     *int   `json:"limit"`
}

type helpDocument struct{ id, title, keywords, body string }
type helpEntry struct{ topic, parameter, title, keywords, body string }
type helpHit struct {
	Topic     string `json:"topic"`
	Parameter string `json:"parameter,omitempty"`
	Summary   string `json:"summary"`
}

// Static documentation only. Never index tool results, credentials or local
// files. Parameter details are read from the same definitions used at runtime.
var helpTopics = []helpDocument{
	{"tasks", "Task lifecycle and exit results", "任务 async sync handle task running exit_status", fmt.Sprintf(`Every exec has a distinct SSH channel/taskN ID, output cursor and exit state. collect_output=true (default) waits synchronously; only concurrent synchronous starts on the same session exclude each other (busy/not_sent, never queued). Every return releases that slot, even when the old program still runs. Async starts (collect_output=false) can run alongside sync/async tasks, return a handle immediately, and create no background timeout timer. Normal sync completion with all output returned omits handle; use task_list to find the retained record. Early/uncertain starts or remaining output retain a handle. Use task_status without consumption; task_wait/read collect incrementally; task_close_stdin sends EOF, task_signal requests a signal, task_release deletes only completed idle records. State is starting/running/finished/unknown. finished means channel completion, not success: inspect exit_code, exit_signal and warning. Missing exit-status is unknown outcome, not automatically a transport failure; never replay. Exit-signal is separate, not a synthesized remote exit_code. One output reader per task. Limits: %d unfinished/%d retained per session, %d unfinished/%d retained globally, 1MiB history/task. Completed records expire after %d minutes or earlier capacity eviction; running/unknown records are not capacity-evicted. Close/reconnect discards task handles/output without guaranteeing program termination.`, core.MaxRunningTasks, core.MaxTasks, core.MaxGlobalRunningTasks, core.MaxGlobalTasks, int(core.TaskRetention.Minutes()))},
	{"connections", "Connections and mode selection", "连接 登录 ssh telnet stream exec 认证 跳板", `SSH server commands: connect with mode=exec, then exec. SSH network devices, persistent shells and Telnet: mode=stream, observe prompts using read_until, then sendline_text. Telnet always stream; credentials/login input are never sent automatically. SSH host-key verification defaults off, no known_hosts discovery/writes. Explicit host_key or known_hosts enables verification; insecure=true overrides both. A successful connect does not prove downstream console login readiness. Never replay input or add Enter merely because output is empty. Use list/get_config to inspect; reconnect explicitly replaces the connection, reuses private original authentication/current settings, discards buffers/tasks and never replays application input. Close discards all buffers and reconnect parameters, not a guarantee that remote programs stopped.`},
	{"timeouts", "Wait budgets and continued collection", "rpc timeout slow output 超时 等待 term signal budget", `Each RPC has a 115s budget. Read defaults: total=30000ms, idle=500ms; either condition returns, preserving the connection. read_total_timeout_ms max 110000; for slow streams, read_idle_timeout_ms=0 disables silence termination while total still bounds waiting; normal read timeouts/cancellation/output limits never send TERM. exec/task_wait ignore read timeouts/end_pattern and use exec_total_timeout_ms (default 120000, range 1..300000). RPC budget can end observation earlier: retain the handle and continue task_wait/read; never relaunch the command. Each task_wait gets a fresh budget. timeout_source distinguishes rpc_budget, caller_deadline, exec_wait and read_total. wait_elapsed_ms measures this collection; started_at/task_elapsed_ms measure locally acknowledged task lifetime, frozen on channel completion. Only configured exec_wait timeout may attempt TERM when term_on_timeout=true (default). false only stops waiting. TERM sent is a local request-write receipt, not delivery or exit. No automatic KILL. Explicit task_signal is read_only-guarded; stalled control writes close shared transport and leave outcomes unknown. Example: task_wait({"handle":"task1","exec_total_timeout_ms":10000,"term_on_timeout":false}).`},
	{"output", "Output history, ANSI and filters", "输出 日志 过滤 ansi history cursor 分页", `Output is incremental by default. Per-task history is 1MiB, default page 64KiB, max_output_bytes <=512KiB UTF-8. read_history(offset) and read_tail(lines=20) do not move the incremental cursor; use end as the next raw offset. dropped_bytes reports overwritten history, more reports unread bytes. Empty output is not evidence of no execution/authentication. strip_ansi defaults false; include_pattern/exclude_pattern default empty (Go RE2, max1024 bytes). Strip ANSI CSI/OSC first, then include/exclude lines. Raw history/offsets stay intact; rejected lines advance cursor. end_pattern still matches original decoded text before projection, and is ignored by exec. Projection scans at most64KiB raw/page; scan_limit means continue reading. Lines >16KiB use bounded fragments (line_fragments=true). Kept lines/fragments are atomic: small pages report minimum_output_bytes. Incomplete characters/lines/ANSI may stay unread; read_until can await more data. view_incomplete means an unfinished control sequence hit a scan/pattern boundary: explicitly disable projection to inspect raw history instead of repeating identical reads. filtered_bytes/filtered_lines/stripped_bytes describe projection, separate from dropped_bytes. Raw inspection: read_history with strip_ansi=false, include_pattern="", exclude_pattern="". Example: read_until({"handle":"task1","strip_ansi":true,"include_pattern":"ERROR|WARN","read_total_timeout_ms":5000}).`},
	{"ssh_jumps", "SSH jumps and forwarded targets", "跳板 隧道 转发 ssh_jumps tunnel telnet hop", `Order: local -> ssh_jumps[0] -> ssh_jumps[1] -> target host/port. Up to8 SSH hops, port defaults22 per hop. Each hop independently supplies username and password/keyfile, optional key_passphrase and host_key/known_hosts/insecure. Credentials and verification policies are not inherited. Every jump must permit direct-tcpip forwarding; no shell ssh commands are executed. protocol selects the final target: SSH supports exec/stream, Telnet stream only, using the internal Telnet codec. One connect_timeout_ms covers all forwarding/handshakes. Failures report 1-based hop_index (target=hop-count+1), host/port, from_host (local or prior configured host, not an inferred egress IP), stage and target. SSH keepalive probes the final SSH endpoint; for Telnet this is the last SSH jump, not proof of Telnet application readiness. Peer metadata is a software/key clue, not physical identity or system-version proof; SSH reconnect reports host_key_changed if the target fingerprint changed. Telnet targets have no SSH peer identity; the last network leg is plaintext. Existing ssh -L listener addresses can be connected normally; keepsess does not manage new -L/-R/-D listeners. Old jumps is rejected. Example: connect({"protocol":"telnet","host":"switch.internal","port":23,"ssh_jumps":[{"host":"bastion","username":"user","keyfile":"/path/key"}]}).`},
	{"profiles", "Explicit connection profiles", "连接 配方 profile 冷启动 重启", `API contract 2 tools have no sess_ prefix; clients may add keepsess_. Explicit named recipes live in ~/.keepsess.toml [profiles.NAME], loaded at Agent startup. Each recipe requires protocol, host, port and explicit mode; SSH requires username and password/keyfile (password/keyfile can be supplied at connect). SSH jumps use [[profiles.NAME.ssh_jumps]], nearest first, up to 8. keyfile/known_hosts paths may start ~/. Use connect({"profile":"server","name":"prod"}) to create a NEW connection; never restores a live session, terminal state or application input. No automatic profile selection and no last profile. Do not mix profile with protocol/host/port/mode/username/ssh_jumps/host verification; name, configuration and target password/keyfile/key_passphrase overrides are allowed. Target key passphrase is supplied only per call; recipes exclude private key content and key passphrases. Jump keys must not require a passphrase. Configuration containing profiles must be private (0600 on Unix); optional target/jump passwords are plaintext and must not be published. Recipes are separate from connection history and unverified manual Telnet credentials. initialize._meta/list expose only available profile names and api_contract. Unknown references do not prove process restart: inspect list/task_list and instance_id; never replay earlier input.`},
	{"naming", "Names and process identity", "命名 重命名 name rename instance_id 句柄", `connect/exec accept optional name aliases; stable sN/taskN IDs remain valid and are returned separately from name. rename(session,name) and task_rename(handle,name) change aliases; old aliases stop resolving. Names: 1..64 ASCII alphanumeric/._-, start alphanumeric; sN/taskN forms reserved. Session and global retained task names use separate unique namespaces. Pending connects reserve names; task names are atomically reserved before exec requests, so duplicates send nothing. Aliases can be reused after records are released: prefer stable IDs for long operations. All 30 tools are always discoverable with complete parameter documentation; no profile switch is needed. configure({"session":"s1"}) without patches inspects effective settings. After the first successful connect, call configure({"instance_name":"short task label"}) without session/settings. Instance names allow 1..64 printable Unicode characters, no surrounding whitespace; they label the Agent process in Web and archives, do not alias sessions, and do not change instance_id. Every process has a short random instance_id, shown in initialize._meta, connect/reconnect, lists and unknown-handle errors. Missing handles may mean closure, expiry or instance change: inspect lists, never replay. No mandatory instance parameter or expect tool. Example: rename({"session":"s1","name":"prod"}); exec({"session":"prod","command":"make","name":"build","collect_output":false}).`},
	{"exec_environment", "Bash login, cwd and env", "环境变量 登录shell bash login cwd env profile 工作目录", `exec defaults login=true: remote bash --login -c, noninteractive stdin EOF and no implicit PTY. Missing Bash fails without fallback or retry. Profiles may emit output/run initialization; Bash reads /etc/profile and the first available user login profile, not necessarily .bashrc or all profiles. Then explicit env exports, cwd change, command. Export/cd failure prevents command execution. env accepts up to128 string values, shell identifier keys; values and cwd are quoted and env values are audit-redacted. Settings are per-task, not persistent shell state. login=false and no cwd/env sends command unchanged; with cwd/env uses bash -c without login initialization. The encoded wire command including wrapping is subject to max_input_bytes. Example: exec({"command":"make test","cwd":"/srv/project","env":{"MODE":"debug"},"collect_output":false}). For persistent terminal state use stream, not repeated exec. Never infer a safe retry from empty output.`},
	{"interactive", "Interactive exec stdin and PTY", "交互 stdin eof pty gdb 输入", `Exec startup defaults interactive=false (stdin EOF), pty=false. interactive=true retains task stdin before startup; pty=true requires interactive and requests vt100 PTY before execution. Cannot enable either later. PTY changes echo/line discipline and may merge streams, but does not change collection/timeouts. Send/read use the exec task handle only, never the parent session. One writer per task, one reader; writing with collect_output=false can run alongside a wait/read. task_close_stdin(handle) sends SSH EOF, is idempotent and does not consume output or stop the task. Ctrl-D bytes are not SSH EOF. All task input/EOF respects current read_only, encoding, byte limits/backpressure and write deadline. A stalled/cancelled write closes shared transport, leaves unknown input outcome and is never replayed. Example: exec({"command":"gdb program","interactive":true,"pty":true,"collect_output":false}); sendline_text({"handle":"task1","data":"bt","line_end":"\\n"}); task_close_stdin({"handle":"task1"}).`},
	{"read_only", "Read-only guard and write receipts", "只读 read_only not_sent sent written_bytes 密码 回执", `read_only defaults false and is set only by connect/configure, never by per-operation override (true or false both rejected). It blocks send/exec and manual/automatic signals before application writes, without consuming output; returns not_sent/read_only. Protocol negotiation and keepalive continue; existing remote programs are not stopped. Changing it during application writes returns busy and leaves the entire patch unchanged; writers recheck live state before writing. It is an accidental-input guard, not an authorization boundary. Stream sent/written_bytes confirm only local writer completion, not endpoint delivery, password acceptance or shell readiness. unknown written_bytes is a confirmed lower bound. Empty output/start=end and observed prompts cannot establish input/output causality; never resend credentials or add Enter solely on silence. Example: configure({"session":"s1","read_only":true}).`},
	{"configuration", "Configuration defaults and overrides", "配置 参数 configure encoding 编码 limits", `All settings are top-level. connect/configure change session defaults; operation settings are snapshots for that call, except the live read_only check. No global pollution. Connection-only settings (read_only, connect_timeout_ms, session_idle_timeout_ms, tcp_keepalive_ms, ssh_keepalive_ms, ssh_keepalive_timeout_ms) cannot be overridden per operation. Legacy nested config is accepted for compatibility; top-level values win. Tools/list shows only applicable settings, while ignored legacy overrides keep their original runtime behavior. Numeric settings accept integers or bounded arithmetic strings such as "30*60*1000" (no eval/calls, integer division only, <=128 characters). Unknown/null/invalid/out-of-range patches fail atomically. get_config returns effective defaults/units/platform capabilities; help(topic="configuration",parameter="read_idle_timeout_ms") returns one setting definition. Encoding supports UTF-8/GBK/Shift-JIS. TCP queue backpressure is available on Linux; nonzero threshold unsupported on Windows. All waits, queues and history remain bounded.`},
}

func helpToolSchema() map[string]any {
	return map[string]any{"name": "help", "description": "Built-in reference help, optional for tool use: no arguments lists a short directory; topic returns a workflow or full tool schema; topic+parameter returns one field; query searches short English/Chinese snippets (default 3, limit 1..5). query is exclusive with topic/parameter. Only static built-in documentation is searched; no files, sessions, credentials or network accessed and no runtime state changed.", "inputSchema": schema(map[string]any{
		"topic":     str("Tool name or topic from directory; omit for directory."),
		"parameter": str("One parameter within topic; requires topic to be a tool name or configuration. Omit to return the whole topic."),
		"query":     map[string]any{"type": "string", "maxLength": 512, "description": "English/Chinese full-text search of built-in documentation only; at most 512 UTF-8 bytes; search processes at most the first 16 whitespace-separated words. Exclusive with topic/parameter. Returns bounded snippets, or matched=false when no match; expand a result with topic/parameter."},
		"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 5, "default": 3, "description": "Maximum query search results, default 3. Used only for query search; snippets are about 220 characters each."},
	})}
}

func helpProperties(tool string) map[string]any {
	props := definitions[tool]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	result := make(map[string]any)
	config := core.ConfigDescriptions()
	for key, value := range props {
		if _, isConfig := config[key]; !isConfig || applicableConfig(tool, key) {
			result[key] = documentedField(key, value.(map[string]any))
		}
	}
	return result
}

func documentedField(key string, source map[string]any) map[string]any {
	field := cloneMap(source)
	if _, config := core.ConfigDescriptions()[key]; config {
		b, _ := json.Marshal(core.DefaultConfig("ssh"))
		var defaults map[string]any
		_ = json.Unmarshal(b, &defaults)
		if key == "line_end" {
			field["description"] = fmt.Sprintf("%s Defaults: SSH `%s`; Telnet `%s` (logical visible-escape strings).", source["description"], core.DefaultConfig("ssh").LineEnd, core.DefaultConfig("telnet").LineEnd)
		} else {
			field["default"] = defaults[key]
		}
		field["description"] = field["description"].(string) + " On connect, omission uses the protocol default; configure leaves omitted fields unchanged; operation overrides apply only to this call and otherwise inherit the session's current settings."
	}
	return field
}

func helpDirectory() []map[string]string {
	out := make([]map[string]string, 0, len(toolOrder)+len(helpTopics))
	for _, topic := range helpTopics {
		out = append(out, map[string]string{"topic": topic.id, "summary": topic.title})
	}
	for _, name := range toolOrder {
		out = append(out, map[string]string{"topic": name})
	}
	return out
}

var indexedHelp = helpIndex()

// Index a configuration field once rather than returning the same description
// from twelve tools. Tool-specific fields (env, PTY, etc.) retain their context.
func helpIndex() []helpEntry {
	entries := make([]helpEntry, 0, 100)
	for _, topic := range helpTopics {
		entries = append(entries, helpEntry{topic: topic.id, title: topic.title, keywords: topic.keywords, body: topic.body})
	}
	config := configSchema(false).(map[string]any)["properties"].(map[string]any)
	keys := sortedKeys(config)
	for _, key := range keys {
		entries = append(entries, helpEntry{topic: "configuration", parameter: key, title: key, body: config[key].(map[string]any)["description"].(string)})
	}
	seen := map[string]bool{}
	for _, name := range toolOrder {
		props := helpProperties(name)
		entries = append(entries, helpEntry{topic: name, title: name, keywords: strings.Join(sortedKeys(props), " "), body: definitions[name]["description"].(string)})
		for _, key := range sortedKeys(props) {
			if _, global := config[key]; global {
				continue
			}
			field := props[key].(map[string]any)
			desc, _ := field["description"].(string)
			fingerprint := key + "\x00" + desc
			if desc != "" && !seen[fingerprint] {
				seen[fingerprint] = true
				entries = append(entries, helpEntry{topic: name, parameter: key, title: key, body: desc})
			}
		}
	}
	return entries
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func helpTokens(query string) []string {
	query = strings.ToLower(query)
	query = strings.NewReplacer("慢输出", " slow output ", "重连", " reconnect ", "连接", " connect ", "会话", " session ", "任务", " task ", "关闭", " close ", "密码", " password ", "凭据", " credentials ", "信号", " signal ", "保活", " keepalive ", "终端", " terminal ", "错误", " error ", "配置", " configuration ", "历史", " history ", "回显", " echo ", "重试", " retry ", "环境变量", " env ", "工作目录", " cwd ", "超时", " timeout ", "预算", " budget ", "跳板", " ssh_jumps ", "隧道", " tunnel ", "转发", " forwarding ", "只读", " read_only ", "重命名", " rename ", "命名", " naming ", "实例", " instance_id ", "过滤", " filter ", "颜色", " ansi ", "日志", " output ", "输出", " output ", "分页", " history ", "登录", " login ", "交互", " interactive ", "编码", " encoding ").Replace(query)
	parts := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	seen := map[string]bool{}
	out := []string{}
	for _, part := range parts {
		if !seen[part] {
			seen[part] = true
			out = append(out, part)
			if len(out) == 16 {
				break
			}
		}
	}
	return out
}

func helpSnippet(text string, terms []string) string {
	lower := strings.ToLower(text)
	position := -1
	for _, term := range terms {
		if at := strings.Index(lower, term); at >= 0 && (position < 0 || at < position) {
			position = at
		}
	}
	start := 0
	if position >= 0 {
		start = utf8.RuneCountInString(lower[:position]) - 50
		if start < 0 {
			start = 0
		}
	}
	runes := []rune(text)
	end := start + 220
	if end > len(runes) {
		end = len(runes)
	}
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

func searchHelp(query string, limit int) []helpHit {
	terms := helpTokens(query)
	type ranked struct {
		entry          helpEntry
		matches, score int
	}
	rankedEntries := []ranked{}
	for _, entry := range indexedHelp {
		r := ranked{entry: entry}
		for _, term := range terms {
			score := 0
			if entry.parameter == term {
				score += 120
			}
			if entry.topic == term {
				score += 80
			}
			if strings.Contains(strings.ToLower(entry.title), term) {
				score += 20
			}
			if strings.Contains(strings.ToLower(entry.keywords), term) {
				score += 10
			}
			if strings.Contains(strings.ToLower(entry.body), term) {
				score++
			}
			if score > 0 {
				r.matches++
				r.score += score
			}
		}
		if r.score > 0 {
			rankedEntries = append(rankedEntries, r)
		}
	}
	sort.SliceStable(rankedEntries, func(i, j int) bool {
		a, b := rankedEntries[i], rankedEntries[j]
		if a.matches != b.matches {
			return a.matches > b.matches
		}
		return a.score > b.score
	})
	if len(rankedEntries) > limit {
		rankedEntries = rankedEntries[:limit]
	}
	results := make([]helpHit, 0, len(rankedEntries))
	for _, r := range rankedEntries {
		results = append(results, helpHit{Topic: r.entry.topic, Parameter: r.entry.parameter, Summary: helpSnippet(r.entry.body, terms)})
	}
	return results
}

func builtinHelp(r helpRequest) (any, error) {
	if len(r.Query) > 512 || len(r.Topic) > 128 || len(r.Parameter) > 128 {
		return nil, errors.New("help query max512 bytes; topic/parameter max128 bytes")
	}
	r.Topic = strings.TrimSpace(r.Topic)
	r.Parameter = strings.TrimSpace(r.Parameter)
	r.Query = strings.TrimSpace(r.Query)
	limit := 3
	if r.Limit != nil {
		limit = *r.Limit
		if limit < 1 || limit > 5 {
			return nil, errors.New("help limit must be 1..5")
		}
	}
	if r.Query != "" {
		if r.Topic != "" || r.Parameter != "" {
			return nil, errors.New("use query for search OR topic/parameter for detail, not both")
		}
		results := searchHelp(r.Query, limit)
		return map[string]any{"results": results, "matched": len(results) > 0, "next": "Expand a result with help(topic=...,parameter=...). No match: help() lists topics."}, nil
	}
	if r.Topic == "" {
		if r.Parameter != "" {
			return nil, errors.New("parameter requires topic")
		}
		return map[string]any{"topics": helpDirectory(), "next": "Use topic for one document, topic+parameter for one field, or query for short full-text search."}, nil
	}
	if def, ok := definitions[r.Topic]; ok {
		props := helpProperties(r.Topic)
		if r.Parameter != "" {
			field, found := props[r.Parameter]
			if !found {
				return nil, fmt.Errorf("parameter %q is not applicable to %s; query help(topic=%q) for supported fields", r.Parameter, r.Topic, r.Topic)
			}
			return map[string]any{"topic": r.Topic, "parameter": r.Parameter, "schema": field}, nil
		}
		input := cloneMap(def["inputSchema"].(map[string]any))
		input["properties"] = props
		return map[string]any{"topic": r.Topic, "description": def["description"], "inputSchema": input, "next": "For smaller output request topic+parameter. For workflows search query or inspect a semantic topic."}, nil
	}
	for _, topic := range helpTopics {
		if topic.id == r.Topic {
			if r.Parameter != "" {
				if topic.id != "configuration" {
					return nil, errors.New("parameter detail requires a tool topic or configuration")
				}
				props := configSchema(false).(map[string]any)["properties"].(map[string]any)
				field, ok := props[r.Parameter]
				if !ok {
					return nil, fmt.Errorf("unknown configuration parameter %q", r.Parameter)
				}
				return map[string]any{"topic": r.Topic, "parameter": r.Parameter, "schema": documentedField(r.Parameter, field.(map[string]any))}, nil
			}
			return map[string]any{"topic": topic.id, "title": topic.title, "text": topic.body}, nil
		}
	}
	return nil, fmt.Errorf("unknown help topic %q; use help() or query", r.Topic)
}
