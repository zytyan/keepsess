---
name: keepsess
description: Operate persistent SSH exec or SSH/Telnet console streams through keepsess 0.3, with encoded text, explicit byte escapes, bounded reads and session configuration.
---

# keepsess 0.3 operating skill

Start MCP stdio with `keepsess serve`. Use `keepsess version` or MCP initialize to check the
build identity; the 0.3 API replaces sess_interact/sess_read and old wait/input_id parameters.
Optional audit: `serve --log /private/path/audit.jsonl`. Authentication fields are redacted;
commands and send data are recorded and can contain secrets. SSH host-key verification is optional and skipped by default;
provide known_hosts or host_key explicitly when verification is required.

## Human Web terminal and persistent history

`serve` also starts an embedded offline xterm.js interface, default `127.0.0.1:8765`.
Occupied ports are retried; inspect `initialize._meta.web` or `sess_list.web` for the
actual URL and recording health. Startup notices use stderr only. `--web=false`
disables Web/history; never start another process just to obtain the current URL.
The browser lists stable session IDs plus model-assigned aliases, retains closed
sessions and offers older instance archives. Archives do not restore connections.
Browser input is read-only until explicitly unlocked and returns to read-only on
refresh/reconnection. Unlocking never overrides session `read_only`. Human writes
share core write limits and receipts, and are never automatically retried.
Terminal scrollback defaults to 5000 lines, adjustable and saved in that browser;
this does not limit persistent disk history or change MCP output cursors. Tasks
appear in a secondary sidebar, ordered by creation. Output follows the viewport
only at the bottom. A new task is selected automatically only while viewing the
latest task at its bottom; reading older output/tasks keeps the current selection.
Pending input/receipts pause automatic switching; switching restores read-only.
A read-only session overview can display several commands and their outputs together.
It pages at most 20 task previews, caps each expanded terminal at 12 rows and divides
its scrollback budget across previews. Decoder/ANSI state stays task-local; PTY tasks
link to their individual terminals. Reading older content holds position; at the tail,
new output/tasks follow automatically. Overview observation never sends terminal input.

History defaults to the OS user config directory under `keepsess/history`, with
private instance directories/files. Commands, raw terminal input (including passwords)
and output are retained; structured connection credentials and env fields are excluded.
Do not publish history directories. `--history-dir` changes the location and
`--history-max-bytes` enables a capacity guard; default 0 retains without automatic
cleanup. Recording failures/gaps are reported without blocking remote connections.
For reverse proxies use `--web-base-path /keepsess/` if the prefix is forwarded;
if stripped, retain internal `/` and set `--web-public-url https://host/keepsess/`.
Use `--web-listen` for explicit nonlocal binding; external authentication belongs
to the proxy. No MCP tool or Web observation may replay input after a lost receipt.

## Install only when requested

`keepsess install [FILE] --client claude|cursor|vscode|codex` installs the executable, MCP entry
and embedded skill. FILE is the target client config; omitted means the selected client's
project config. Default client auto/no file uses Claude Code .mcp.json. Standard .cursor/mcp.json,
.vscode/mcp.json and .codex/config.toml paths are recognized; --client resolves ambiguity.
Use --dry-run to inspect destinations without writes, --dir for managed binaries, and
--skill-dir for a nonstandard skill location. Custom/global config paths need the intended
skill directory explicitly when it differs from project defaults.

The installer preserves other servers, comments, existing keepsess serve arguments/env and
permission settings; changed files get private backups. It copies the executable into a
content-addressed directory, so the original download can be moved. It does not start services
or approve/trust MCP tools. Reload the client and use its own trust flow. Generic JSON clients
must support the selected skill discovery directory. Unsupported TOML layouts or conflicting
keepsess entries fail before writes. Never publish config backups that may contain secrets.
All 30 tools are discovered through MCP tools/list, not individually registered.

## Progressive built-in help

Tools/list uses concise descriptions and advertises only applicable settings, retaining types, defaults, target exclusivity and essential safety rules. Routine calls do not require loading this full skill or invoking help. For uncertain details use sess_help(): no arguments returns a compact directory; topic="sess_exec" returns one tool's full schema; topic="sess_exec", parameter="env" returns one parameter. Semantic topics: connections, tasks, timeouts, ssh_jumps, naming, exec_environment, interactive, read_only, output, configuration. Configuration fields can be queried with topic="configuration", parameter="line_end".

Use query="RPC 超时" or query="跳板 telnet" for built-in keyword/full-text search (English/Chinese aliases). Default 3 results, limit 1..5, snippets about220 characters; expand the returned topic/parameter rather than requesting all help. Query max512 bytes/16 terms; query is exclusive with topic/parameter, and parameter requires topic. No match returns matched=false. Help reads static built-in documentation only, never endpoint output, credentials, files or network; it changes no session state. Full help and defaults come from the runtime definitions. Older ignored per-operation config overrides remain accepted, but are omitted from discovery/help; meaningful options stay available at the top level. Numeric settings still accept bounded arithmetic strings.

## Connect and configure

Session/task names: connect and exec accept optional name. Names are mutable aliases; results keep stable sN/taskN IDs plus name. References accept either ID or current alias. sess_rename(session,name) and sess_task_rename(handle,name) change aliases without reconnecting or restarting; old aliases stop resolving. Names are 1..64 ASCII alphanumeric/._-, start alphanumeric, and may not use reserved sN/taskN forms. Session names and global task names have separate namespaces; duplicates (including concurrent starts) are rejected before application input. Names expire with retained records and can be reused; prefer stable IDs for long workflows.

Each process has a short random instance_id, shown in initialize._meta, connect/reconnect, lists and unknown-handle errors. No expected_instance_id parameter or mandatory check is needed. Missing records may mean closure/expiry or an instance change: inspect lists, never replay commands/login inputs. No automatic recovery or sess_expect tool.

All tool input parameters are top-level, including timeouts, encoding, line_end, output limits and read_only. Never wrap settings in config. Connect/configure set session defaults; operation-level settings only affect that call. Connection-only/read_only settings remain restricted to connect/configure. Legacy nested config is accepted only for compatibility; top-level values take precedence.

`sess_connect` declares protocol, host and port in inputSchema.required. For SSH, conditional
required also requires username and at least one of password/keyfile (both are allowed),
and optionally known_hosts or pinned host_key; mode defaults to exec. Choose mode="stream" for network
consoles AND interactive shells, including interactive Bash. Telnet only supports stream and is plaintext.
Use stream send/read for persistent terminal shells, console logins and all Telnet. Explicit
interactive exec tasks are supported on SSH mode=exec with their own handle. Selecting
SSH transport does not make an interactive session an exec session. Do not use exec as a probe
or fallback when a stream is silent. Always use the actual short session
handle returned (s1, s2, ...); do not select a session implicitly. IDs are not reused in-process.
Connect returns no terminal output and does not consume the greeting.
SSH skips host-key verification by default and does not read/write known_hosts automatically.
Provide host_key or known_hosts explicitly to enable verification; failures are errors. Legacy
insecure=true explicitly skips verification even with verification fields. Priority: insecure=true,
host_key, known_hosts, default skip. insecure=false alone does not enable verification.

```json
{"read_idle_timeout_ms":500,"read_total_timeout_ms":30000,"protocol":"telnet","host":"switch.example","port":23}
```

For SSH or Telnet targets, optional top-level ssh_jumps is an ordered array of up to 8 SSH hops, nearest first. Each hop takes host, username, password and/or keyfile; port defaults 22. key_passphrase, host_key, known_hosts and insecure work independently with the same verification precedence as the target. No inherited credentials or host-key policies. Order example: local -> ssh_jumps[0] -> ssh_jumps[1] -> target. Errors identify 1-based hop_index (target is hop-count+1), host/port, from_host (logical configured previous host, first is local), stage and target. direct-tcpip supports SSH/Telnet stream; existing -L listener addresses can also be connected directly. No new listener-management tools. Requires direct-tcpip forwarding on every jump; no remote shell ssh commands. protocol selects the final target: SSH supports exec/stream; Telnet requires stream and uses the internal Telnet codec. The old jumps parameter is rejected. One connect_timeout_ms covers the entire chain, including forwarding/authentication. Failure/cancellation closes the chain, without retries. Explicit reconnect privately reuses all hops/current configuration; close releases them. Only SSH targets expose final target peer identity; Telnet targets never report a hop as their peer; audit recursively redacts hop passwords/passphrases. SSH keepalive traverses the chain to the SSH target or, for Telnet, the final SSH jump; this does not prove Telnet login/application readiness; TCP settings/backpressure apply to the first TCP socket.

Example sess_connect:
```json
{"protocol":"ssh","host":"target.internal","port":22,"username":"target-user","keyfile":"/path/to/target_key","ssh_jumps":[{"host":"jump1.example","username":"jump-user","keyfile":"/path/to/jump1_key"},{"host":"jump2.internal","username":"jump-user","keyfile":"/path/to/jump2_key"}],"connect_timeout_ms":30000}
```

For a Telnet target, no target SSH username/password is required. Observe prompts and explicitly send Telnet login input through stream tools; never auto-replay credentials. The final jump-to-Telnet leg is plaintext. Example:
```json
{"protocol":"telnet","host":"switch.internal","port":23,"ssh_jumps":[{"host":"bastion.example","username":"jump-user","keyfile":"/path/to/jump_key"}],"connect_timeout_ms":30000}
```

`sess_configure(session, ...settings)` atomically updates only supplied fields. `sess_get_config`
returns effective values, descriptions, units and capabilities, without credentials. All send,
exec and read tools accept top-level settings for that call only; active operations retain their
snapshot except the live read_only guard, which is rechecked before writing. read_only, connect_timeout_ms, session_idle_timeout_ms, tcp_keepalive_ms, ssh_keepalive_ms and ssh_keepalive_timeout_ms cannot be overridden
per operation. Changing connect_timeout on an established session stores it for the next
sess_reconnect; explicitly pass those settings again for a new sess_connect.

Defaults: read total 30000ms; read idle 500ms; exec total 120000ms; connect/write 10000ms; unused session 7200000ms;
TCP keepalive 15000ms; max input 4096 bytes; max output 65536 bytes; encoding utf-8; end_pattern
empty (disabled). Idle read/session timeout and keepalive accept 0 to disable; total read must
be 1..110000ms; exec_total_timeout_ms is 1..300000ms. One RPC is capped at 115s. Numeric config accepts integers or simple arithmetic
strings such as "30*60*1000", without function calls or arbitrary evaluation.

## Explicit reconnect

sess_reconnect({"session":"s1"}) closes the old transport and reuses its private original
connection parameters plus CURRENT session config (including read_only and connect_timeout_ms).
The handle remains s1. SSH authentication runs normally; key/known_hosts paths are reread.
It NEVER sends terminal Enter, Telnet credentials, setup commands or historical application
input. It discards all old task handles and buffers/cursors; new output starts at offset 0 and remains unread.
Persistent shell cwd/environment, device view and terminal login state are not restored.
Read and inspect the new stream before deciding what to send.

Active reads/writes/configure/reconnect or running exec return busy without disconnecting.
Failed/cancelled/timed-out reconnect retains the disconnected handle and parameters for an
EXPLICIT retry, never automatic replay. close/close_all cancel an in-progress reconnect and
release the handle/parameters; a closed/expired handle needs a new sess_connect. Credentials
are private process memory, absent from list/get_config/reconnect results. Reconnect does not
imply device state reset; a console server may still expose the same downstream terminal.

## Connection health and peer identity

Default session idle retention is 2 hours without tool operations, not a fixed connection age.
Active tasks/readers/writers prevent reclamation. SSH uses both OS TCP keepalive and protocol
keepalive@openssh.com every 30000ms, with ssh_keepalive_timeout_ms=10000 for each reply.
ssh_keepalive_ms=0 disables it. Negative replies prove responsiveness too. Only one probe is
outstanding; deadline closes transport and marks unfinished tasks unknown. In-flight probes
keep their deadline when configured. Probes never send terminal input or refresh local idle.
They cannot prevent reboots or terminal application login timeouts; direct Telnet uses TCP only; Telnet via ssh_jumps additionally probes the last SSH jump.

On transport failure inspect disconnect_reason/error; prefer sess_reconnect when the handle
is retained. reset_by_peer, transport_timeout, keepalive_timeout, ssh_disconnect, remote_eof
and transport_error describe evidence, not guaranteed device root causes. EOF does not prove
an idle timeout or reboot. Reclaimed/closed handles return session_idle_expired/explicit_close
with next_action=sess_connect. Up to 128 credential-free reason records last 2h, possibly less
under capacity pressure. unknown_handle may mean a released/expired handle or service restart;
do not infer a device reset or replay earlier commands.

SSH connect/reconnect includes peer.server (bounded software banner) and peer.host_key (SHA256).
Only when changed on the same handle: peer.host_key_changed=true. Ordinary read/send/list omit
identity to save tokens. This is observational metadata, not proof of physical device identity,
Linux image version or reboot. Default host verification policy is unchanged. No automatic
uname or shell probes. A failed reconnect never presents a cached identity as a new observation.

Completion waits default to 120s and accept up to 300s; a single RPC still has a 115s budget.
If the RPC budget expires first, return the task handle WITHOUT TERM and continue via
sess_task_wait/read. Only the configured completion-wait timeout can trigger term_on_timeout;
it is not a background runtime timer, and collect_output=false still creates no runtime timer.

## Output projection

All collecting tools support strip_ansi=false by default, include_pattern and exclude_pattern (Go RE2, max 1024 bytes). ANSI removal precedes line filters; raw history and byte offsets are preserved. end_pattern matches the original decoded text before projection; exec still ignores end_pattern. Each projected page scans at most 64KiB raw bytes and may return scan_limit; continue with the same handle/session. Lines above 16KiB use bounded fragments (line_fragments=true); complete characters/rendered escapes stay indivisible. Filters return kept lines/fragments atomically: too-small output budget reports minimum_output_bytes. Filtered bytes advance the incremental cursor; empty projected output is not proof the device sent nothing. Counters filtered_bytes/filtered_lines/stripped_bytes describe projection, separate from dropped_bytes history loss. Incomplete lines wait for a line boundary or collection return condition; incomplete ANSI remains unread. view_incomplete on an oversized unfinished control sequence requires explicit raw inspection rather than repeated identical reads. To read raw retained history, set strip_ansi=false and both patterns empty. History remains subject to normal retention/eviction limits.

## Read-only observation

read_only is a boolean session config, default false. Set it on connect or explicitly with
sess_configure: {"read_only":true,"session":"s1"}. It cannot be overridden in
ANY operation (true and false are both rejected). When enabled, all four send tools
and sess_exec return input_state=not_sent with error starting "read_only:"; no application
input is written and no output is consumed. Reads, get_config, list and close remain available.
Manual signals are rejected and automatic timeout TERM reports skipped_read_only. Protocol negotiation and TCP keepalive continue normally.

If an application write is in progress, a configure patch containing read_only returns busy
and applies NONE of the patch. Retry configure explicitly after the write finishes. Successful
enabling serializes with writes; even operations holding an older config recheck the live guard
before sending. Already running remote commands are not stopped; their output remains readable.
Do not disable read_only automatically to retry a rejected send. This is an accidental-input
guard, not authorization: configure can explicitly disable it. Independent connection cursors
do not guarantee the console server broadcasts output or permits concurrent connections.

## Choose exec or stream

| Task | Connection and tools |
|---|---|
| Independent non-interactive SSH command with protocol exit status | SSH mode=exec; sess_exec |
| Interactive Bash/shell, persistent cwd/environment, login dialog, switch/console | SSH mode=stream; sess_sendline_text/sess_sendline_escaped and read tools |
| Explicit interactive process such as GDB | SSH mode=exec; interactive=true, optional pty=true; send/read with task handle |
| Any Telnet connection, including username/password input | mode=stream; send/read tools only, NEVER sess_exec |


- For sess_exec, session may be omitted: infer it only with exactly one retained session, counting disconnected and stream sessions too. No sessions: connect first. Multiple sessions: specify session explicitly; never select by connectivity, mode or recency. A disconnected sole session requires explicit reconnect. Explicit invalid sessions never fall back. Results retain the actual session. Task tools still require handle; task_list() still lists all tasks.
- `sess_exec(command, session?, exec_total_timeout_ms?, term_on_timeout?, ...settings?)`: **SSH mode=exec only**, with stdin EOF by default.
  SSH mode=stream and all Telnet sessions immediately return a tool error without sending
  input; there is no fallback to a shell. Use sess_sendline_text/sess_sendline_escaped for
  interactive streams, never probe the same command with exec first. Uses a fresh SSH channel on the
  persistent transport; shell cwd/variables do not persist. Combined stdout/stderr and protocol
  exit status determine completion, never text markers. If running=true, read the existing
  operation; do not resubmit. Missing exit status is an unknown outcome with warning and no
  exit_code; transport remains usable. exec_finish means channel completion, not success.
  Use top-level exec_total_timeout_ms and term_on_timeout for per-call completion waiting. They override session defaults and legacy nested config values without changing session config.
  **sess_exec ignores read_total_timeout_ms, read_idle_timeout_ms and end_pattern**, from BOTH
  persistent and per-call config. It waits on independent exec_total_timeout_ms (default 120000,
  range 1..300000), measured from collection after command startup; startup still has
  write_timeout_ms and the whole RPC cap remains 115s. Set top-level exec_total_timeout_ms for a
  different execution wait, not a read timeout. Quiet commands and prompt-like text must not
  end the exec wait. collect_output=false returns a process-local task handle (task1, task22, ...)
  after startup, without an execution wait or background runtime timer. Async tasks may run
  concurrently with each other AND with sync calls. Only simultaneous collect_output=true exec
  calls on one session exclude each other: busy/not_sent, no queue. Every sync call releases
  its slot when it returns, even if its program has not exited.
  term_on_timeout defaults true: exec/task_wait completion timeout attempts TERM once and
  returns a handle without waiting for exit. Override false to wait without signalling.
  Read timeouts, collection cancellation, output limits and decode errors NEVER send TERM.
  Signal sent means only request write completion, not remote delivery/exit or process-tree
  termination. No automatic KILL, retry or replay. Signals have a 1s write bound within the
  115s RPC budget; stalled setup/control writes may close the shared transport to unblock I/O,
  affecting all tasks with explicitly unknown outcomes. Ordinary timeouts preserve transport.
  Sync exec_finish with all output returned omits handle (including nonzero exit status).
  Early returns, uncertain startup and remaining output return handle. All executions retain
  bounded records discoverable through sess_task_list. There is no exec silence watchdog.

Exec startup defaults login=true: bash --login -c with noninteractive stdin EOF, no implicit PTY. Missing Bash fails without fallback or replay. Profiles may emit output/run initialization and do not necessarily load .bashrc. Optional env (max 128 string values, shell identifier keys) and cwd run AFTER profiles, before command; failed export/cd prevents the command. Values/paths are shell-quoted; env is audit-redacted. Task settings never persist to the session. login=false sends the original command only if cwd/env omitted; otherwise bash -c applies overrides without login initialization. Wire command including wrapping is subject to max_input_bytes. Do not disable login solely due to silence or retry a possibly started command.

Every task_wait starts a fresh completion-wait budget. Collection returns wait_elapsed_ms; task records expose started_at/task_elapsed_ms (frozen on completion). total_timeout includes timeout_source rpc_budget/exec_wait/caller_deadline; read timeouts can report read_total. Only exec_wait timeout may attempt TERM. After rpc_budget, continue status/read/wait on the same handle, never resubmit; instance change errors call for list inspection.

Task operations (30 tools total):
- sess_task_list(session?): list retained tasks; no output consumption.
- sess_task_status(handle): immediate state, exit information, more, latest signal; no consumption.
- sess_task_wait(handle, exec_total_timeout_ms?, term_on_timeout?, ...settings?): completion wait plus incremental collection; uses exec_total_timeout_ms
  and term_on_timeout, ignoring read timeouts/patterns. Never launches input or takes the sync exec slot.
- sess_task_signal(handle, signal): bounded SSH signal, e.g. TERM/INT/KILL (without SIG prefix).
  read_only blocks manual and automatic signals. request_state is sent/not_sent/unknown,
  skipped_read_only or skipped_finished. Inspect state again to establish completion.
- sess_task_close_stdin(handle): send SSH stdin EOF on an interactive task; no output consumed; read_only guarded.
- sess_task_release(handle): delete a completed idle record and its output. Live, unknown and
  actively accessed tasks return busy. Close the connection to discard all associated handles;
  this does not guarantee remote program exit.

Each task owns a separate output buffer and cursor. All four read tools accept exactly one of
session (stream) or handle (exec); never guess a task from a connection ID. Read config remains
independent of completion wait config. One reader per task; status/signals may run during waits.
State is starting/running/finished/unknown. finished means channel completion; missing exit
status remains an unknown outcome with warning. exit_signal is reported separately from exit_code.
Limits: 8 unfinished tasks/16 retained records per connection; 64 unfinished/128 retained
records globally; 1MiB history per task. Completed records expire after 30min and can be evicted
sooner at capacity, oldest first, excluding active operations. Unknown/live tasks are not evicted
for capacity. Missing/expired handles fail explicitly; never replay because a handle expired.

- Stream/interactive-task send tools: send_text, send_escaped, sendline_text, sendline_escaped (all prefixed sess_).
  They send and collect unread output by default, using bounded deadlines. Set
  collect_output=false to return only a send receipt, e.g. when another call is already reading.
  The observed stream is NOT attributed to the latest input. No input IDs or automatic replay.

`send_text` encodes literal Unicode text with encoding; it does not parse escapes or append bytes.
`send_escaped` decodes ONE visible escape layer after JSON decoding: \0 \r \n \t \xNN produce
bytes, ordinary Unicode and \uNNNN/\UNNNNNNNN use encoding. \\ produces a literal backslash;
Go three-digit octal escapes are also accepted. Unknown/incomplete escapes reject before writing.
HTML entities remain literal. Unencodable input rejects the entire operation, never partial text.

| Logical data value for sess_send_escaped | JSON arguments (serialization example) | Wire bytes |
|---|---|---|
| `\r` (one visible backslash) | `{"session":"s1","data":"\\r"}` | CR (0D) |
| `\x03` | `{"session":"s1","data":"\\x03"}` | Ctrl-C (03) |
| `\0` | `{"session":"s1","data":"\\0"}` | NUL (00) |
| `\\r` (two visible backslashes) | `{"session":"s1","data":"\\\\r"}` | literal backslash + r |

The model's native tool-call format is not necessarily JSON. DeepSeek V4.1's public DSML
reference uses raw parameter text for string="true": its data body should contain visible
\x03, not the extra backslash needed in the JSON serialization example. Objects/arrays marked
string="false" still use JSON. The serving layer/agent performs serialization; do not manually
copy JSON's doubled backslashes into an as-is string parameter. Valid JSON encode/decode pairs
preserve the logical value. keepsess then decodes ONE escape layer for escaped sends only;
text sends do not decode it. DSML is XML-like, not a promise to decode HTML entities: &#13;
remains five literal characters. Never repeatedly pre-decode strings or assume an agent's
additional transformations without inspecting that agent.
Raw and escaped sendline append configured line_end exactly once, even if input already has an
ending. Empty data is allowed. line_end is visible escape text decoded at configuration time
as UTF-8 plus byte escapes: SSH defaults \r, Telnet \r\n. Use sess_send_text/sess_send_escaped without a line ending for pager space, Tab,
or controls that must not append an Enter. Never silently change raw bytes to repair CRLF.

```json
{"end_pattern":"(?m)^<[^>\r\n]+>\\s*$","session":"s1","data":"display version"}
```

Use the above with sess_sendline_text. Supply an observed prompt regex, not a guessed vendor
mode. Adjust to [~host], [*host], or nested views when observed; a pattern match does not prove
command success. Use a pager's observed pattern and sess_send_text data=" " for the next page.
Do not automatically commit/save/discard configurations or answer confirmation questions.

## Interactive exec stdin and PTY

sess_exec defaults interactive=false and pty=false: stdin EOF preserves ordinary command behavior.
For GDB or another interactive process, opt in BEFORE startup:

```json
{"session":"s1","command":"gdb ./app","interactive":true,"pty":true,"collect_output":false}
```

Read the returned handle's prompt, then use any of the four send tools with handle INSTEAD OF
session. Example sess_sendline_text({"line_end":"\\n","handle":"task1","data":"bt"}).
interactive does not change collection or timeout defaults. Prefer collect_output=false for
startup; synchronous collection may also receive concurrent collect_output=false input.
Task input has its own busy writer guard; output has a separate reader guard. Raw/escaped
encoding, line_end, max_input_bytes, backpressure and collection semantics match stream sends.
Never send task input to its parent session: that session is an exec connection, not the stdin.
A task started without stdin cannot enable it later. Inspect interactive/pty/stdin_open in status.

pty=true requires interactive=true and requests vt100 before exec. PTY changes terminal line
discipline, echo, newline and stdout/stderr behavior; servers may merge stderr into terminal
output. Configure line_end explicitly for the program. No PTY is allocated by default.
Without PTY Ctrl-C/Ctrl-D are bytes, not guaranteed signals or EOF; use task_signal for signals.

sess_task_close_stdin({"handle":"task1"}) sends SSH stdin EOF without consuming output or
closing the connection. Repeated closure sends no second request. It does not guarantee exit;
continue read/task_wait. Sending \x04 is not equivalent to closing stdin. Current session
read_only blocks both task sends and EOF. Bounded stalled/cancelled writes close shared transport
and report unknown outcomes for affected tasks; never retry automatically. Ordinary input errors
leave the transport usable but mark stdin unavailable. close_stdin accepts handle only.

## Send receipts and silent login are not authentication results

Keep input_state="sent"; for stream sends it means LOCAL write completion, not device acceptance. Renaming it
to accepted would suggest an acknowledgement the stream protocol cannot provide.

| Receipt | What it proves | What it does NOT prove |
|---|---|---|
| sent (stream) | Local writer completed the full application input | Peer delivery, device application processing, successful authentication, or shell readiness |
| sent (exec startup) | SSH exec channel start request succeeded | Command completion, exit success, or a terminal login result |
| sent (task input / EOF) | Local stdin write / EOF request write completed; EOF has no application-byte count | Program consumption, command success or exit |
| not_sent | This attempt wrote no new application input | The outcome of any earlier attempt |
| unknown | Write/setup failed, timed out or was cancelled; partial delivery/execution is possible | That zero bytes reached the peer |

input_bytes counts intended application bytes after encoding/escapes, including line_end;
written_bytes counts bytes accepted by the local writer, excluding SSH/Telnet framing. In
unknown state it is only a confirmed lower bound; 0/omission is not proof of zero delivery.
TCP queue drain, keepalive, connected=true or even a TCP acknowledgement cannot establish
that the terminal application accepted the password. A stream offers no per-input delivery
or authentication acknowledgement, and output is not attributed to the latest input.

After sending a username/password, output="" or absent output with start==end means this read
consumed no raw output bytes. It does not distinguish a correct password with a silent shell,
a password still pending, a device waiting for a different protocol step, or a failed delivery.
Two empty reads provide no authentication verdict. Echo may be disabled (especially passwords).
Check reason/error/more too: an incomplete encoded character may remain unread.

Do NOT label login successful/failed, automatically resend the username/password, change
CR/LF, or send an extra Enter because output is empty. An Enter is NEW input that can submit
an empty password or replay a command. Prefer one bounded read with no input, an observed
prompt/failure regex, and idle timeout disabled when a quiet device needs more time:

```json
{"read_total_timeout_ms":10000,"read_idle_timeout_ms":0,"end_pattern":"(?im)^(<switch>|Password:|Login incorrect)[[:blank:]]*\\r?$","session":"s1"}
```

Use this with sess_read_until; the regex is illustrative and must match the actual device's
observed text. Match only proves that text appeared; console replay/old unread output can
also match, so a prompt is not protocol-level confirmation of this password attempt. If the
bounded read is still empty, report: "local write completed; remote delivery/authentication/
shell readiness unknown" and inspect the endpoint/console service or ask for evidence before
another login input. Do not endlessly poll or reconnect-and-replay credentials as a guess.

Timeout or cancellation during a write closes transport; cancellation during collection only
stops the read. One writer and one reader may run concurrently; same-direction contention is
busy and does not queue input. Never replay sent/unknown input merely because output is empty.

## Read and manage output

Empty collected output includes comment explaining no new output, a timeout, completion,
partial encoded bytes, an insufficient output budget, a historical gap or an error/closure.
This is an observation, not proof the device sent nothing, the command did not execute, or
login succeeded. No such comment is generated for collect_output=false receipts. Inspect
reason/state/exit information and never resend input or append Enter solely on empty output.

- sess_read_available: immediate unread data, no waiting.
- sess_read_until: unread plus future data, until the first end condition.
- sess_read_tail(lines=20): last retained lines; bounded and does not move incremental cursor.
- sess_read_history(offset?): retained history, not all future output; no cursor change. Page with
  offset=end. Neither historical read waits for more bytes.

End reasons: exec_finish (exec only), match, total_timeout, idle_timeout, aborted, cancelled,
output_limit, closed. Immediate reads need no reason when current data is exhausted. Total
clock starts at read invocation; idle starts then and resets on endpoint bytes. These read
conditions apply to read tools and stream-send collection, not sess_exec. Its total_timeout
is instead governed by exec_total_timeout_ms and may attempt TERM as described above. Ordinary read timeouts leave
connections open. end_pattern matches DECODED unread output, including bytes received before
this send; it does not match reason/error fields. Match consumes only through the match, leaving
later output unread. Never infer input/output pairing or successful execution from it.

Read output is valid UTF-8, without implicit ANSI stripping or EOL normalization. Supported
encodings: utf-8, gbk, shift-jis. Incomplete multibyte characters remain unread until completed;
closed streams/history finalize them under output_decode_error_action. Invalid bytes default
to visible \xNN; to_hex renders <NN>; error reports aborted and leaves the offending byte unread.
A decode error does not itself close transport. Override policy to inspect retained data.

max_output_bytes defaults to 65536 (64KiB), and is 1..524288 (512KiB) UTF-8 display bytes, including escaped invalid-byte text. more=true
means unread bytes remain; use incremental read or read_history offset=end. start/end are RAW byte
offsets, independent of encoding. dropped_bytes reports overwritten history (retention 1MiB).
If minimum_output_bytes is returned, increase output budget to fit the next whole character.

backpressure_bytes=0 disables the optional OS queue threshold. Linux supports SIOCOUTQ queued
bytes (including unacknowledged bytes, entire encrypted transport for SSH); Windows explicitly
rejects a nonzero threshold. Check get_config capabilities. Both platforms still have bounded
writes and SSH flow control. TCP acceptance/queue drain is not remote command completion.

Use sess_list to inspect state. sess_close discards one session and all buffers; sess_close_all
discards every session. Neither returns terminal output; subsequent reads fail. Read first if
output matters. Unused sessions reclaim automatically after session_idle_timeout_ms; remote
closure may happen earlier. Running tasks prevent idle session reclamation; quiet exec tasks have no silence watchdog. If
connected=false, explicitly use sess_reconnect on a retained handle, or sess_connect if the handle
was closed/expired. Inspect remote state before retrying side effects.

## File transfer and forwarding

Use sess_get_file(session,src,dst?) / sess_put_file(session,src,dst) for binary files, backend auto/sftp/scp/exec. Local means the keepsess host; omitted download dst returns an absolute temporary path. Uploads stage beside the destination; failed/unknown operations may leave temporary_path. Never replay automatically. overwrite defaults false; no recursion/resume; 110s budget.

Use sess_tunnel_open(session,type,bind_host?,bind_port?,target_host?,target_port?) for local/remote/dynamic/remote_dynamic TCP forwarding. Bind defaults loopback and port 0. Local/dynamic targets connect from final SSH server; remote/remote_dynamic from keepsess host. Dynamic is SOCKS5 CONNECT only. Returned listen_address is usable by Python on the listening host. sess_tunnel_list(session) inspects; sess_tunnel_close(session,handle) releases listener/connections. Disconnect/reconnect closes tunnels without restoring them. read_only blocks uploads/new tunnels, not existing tunnel traffic. See sess_help for full parameters.
