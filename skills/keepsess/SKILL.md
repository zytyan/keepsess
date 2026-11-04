---
name: keepsess
description: Operate persistent SSH exec or SSH/Telnet console streams through keepsess 0.3, with encoded text, explicit byte escapes, bounded reads and session configuration.
---

# keepsess 0.3 operating skill

Start MCP stdio with `keepsess serve`. Use `keepsess version` or MCP initialize to check the
build identity; the 0.3 API replaces sess_interact/sess_read and old wait/input_id parameters.
Optional audit: `serve --log /private/path/audit.jsonl`. Authentication fields are redacted;
commands and send data are recorded and can contain secrets. Never enable insecure SSH host
verification without explicit user intent.

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
All 15 tools are discovered through MCP tools/list, not individually registered.

## Connect and configure

`sess_connect` requires protocol, host and port. SSH takes username and password/keyfile,
plus known_hosts or pinned host_key; mode defaults to exec. Choose mode="stream" for network
consoles. Telnet only supports stream and is plaintext. Always use the actual short session
handle returned (s1, s2, ...); do not select a session implicitly. IDs are not reused in-process.
Connect returns no terminal output and does not consume the greeting.

```json
{"protocol":"telnet","host":"switch.example","port":23,"config":{"read_idle_timeout_ms":500,"read_total_timeout_ms":30000}}
```

`sess_configure(session, config)` atomically updates only supplied fields. `sess_get_config`
returns effective values, descriptions, units and capabilities, without credentials. All send,
exec and read tools accept a config object for that call only; active operations retain their
snapshot. connect_timeout_ms, session_idle_timeout_ms and tcp_keepalive_ms cannot be overridden
per operation. Changing connect_timeout on an established session only stores the value; explicitly reuse
that config on a new connect.

Defaults: read total 30000ms; read idle 500ms; exec total 30000ms; connect/write 10000ms; unused session 1800000ms;
TCP keepalive 15000ms; max input 4096 bytes; max output 8192 bytes; encoding utf-8; end_pattern
empty (disabled). Idle read/session timeout and keepalive accept 0 to disable; total read must
be 1..110000ms; exec_total_timeout_ms has the same range. One RPC is capped at 115s. Numeric config accepts integers or simple arithmetic
strings such as "30*60*1000", without function calls or arbitrary evaluation.

## Choose exec or stream

- `sess_exec(session, command, config?)`: **non-interactive SSH mode=exec only**.
  SSH mode=stream and all Telnet sessions immediately return a tool error without sending
  input; there is no fallback to a shell. Use sess_sendline_text/sess_sendline_escaped for
  interactive streams, never probe the same command with exec first. Uses a fresh SSH channel on the
  persistent transport; shell cwd/variables do not persist. Combined stdout/stderr and protocol
  exit status determine completion, never text markers. If running=true, read the existing
  operation; do not resubmit. Missing exit status is an unknown outcome with warning and no
  exit_code; transport remains usable. exec_finish means channel completion, not success.
  **sess_exec ignores read_total_timeout_ms, read_idle_timeout_ms and end_pattern**, from BOTH
  persistent and per-call config. It waits on independent exec_total_timeout_ms (default 30000,
  range 1..110000), measured from collection after command startup; startup still has
  write_timeout_ms and the whole RPC cap remains 115s. Set config.exec_total_timeout_ms for a
  different execution wait, not a read timeout. Quiet commands and prompt-like text must not
  end the exec wait. collect_output=false skips waiting and returns a send receipt.
  The independent deadline returns total_timeout without killing the command or closing
  transport. Output budget, cancellation, connection errors and the separate 110s exec silence
  watchdog still apply. If running=true, read the existing command instead of replaying it.
  Explicit follow-up sess_read_until DOES use read timeouts and end_pattern as usual.
- Stream send tools: send_text, send_escaped, sendline_text, sendline_escaped (all prefixed sess_).
  They send and collect unread output by default, using bounded deadlines. Set
  collect_output=false to return only a send receipt, e.g. when another call is already reading.
  The observed stream is NOT attributed to the latest input. No input IDs or automatic replay.

`send_text` encodes literal Unicode text with encoding; it does not parse escapes or append bytes.
`send_escaped` decodes ONE visible escape layer after JSON decoding: \0 \r \n \t \xNN produce
bytes, ordinary Unicode and \uNNNN/\UNNNNNNNN use encoding. \\ produces a literal backslash;
Go three-digit octal escapes are also accepted. Unknown/incomplete escapes reject before writing.
HTML entities remain literal. Unencodable input rejects the entire operation, never partial text.

| JSON arguments for an escaped send | Wire bytes |
|---|---|
| `{"session":"s1","data":"\\r"}` | CR (0D) |
| `{"session":"s1","data":"\\x03"}` | Ctrl-C (03) |
| `{"session":"s1","data":"\\0"}` | NUL (00) |
| `{"session":"s1","data":"\\\\r"}` | literal backslash + r |

This explicit layer supports XML/JSON agent carriers; do not repeatedly pre-decode strings.
Raw and escaped sendline append configured line_end exactly once, even if input already has an
ending. Empty data is allowed. line_end is visible escape text decoded at configuration time
as UTF-8 plus byte escapes: SSH defaults \r, Telnet \r\n. Use sess_send_text/sess_send_escaped without a line ending for pager space, Tab,
or controls that must not append an Enter. Never silently change raw bytes to repair CRLF.

```json
{"session":"s1","data":"display version","config":{"end_pattern":"(?m)^<[^>\r\n]+>\\s*$"}}
```

Use the above with sess_sendline_text. Supply an observed prompt regex, not a guessed vendor
mode. Adjust to [~host], [*host], or nested views when observed; a pattern match does not prove
command success. Use a pager's observed pattern and sess_send_text data=" " for the next page.
Do not automatically commit/save/discard configurations or answer confirmation questions.

Read input_state before retrying: sent means transport accepted input; not_sent means no new
input was written; unknown means partial execution is possible. written_bytes is a confirmed
lower bound when unknown, not evidence of zero delivery. Never replay sent/unknown input merely
because output is empty. Timeout or cancellation during a write closes transport; cancellation
during collection only stops the read. One writer and one reader may run concurrently; contention
within either direction is busy and does not queue input.

## Read and manage output

- sess_read_available: immediate unread data, no waiting.
- sess_read_until: unread plus future data, until the first end condition.
- sess_read_tail(lines=20): last retained lines; bounded and does not move incremental cursor.
- sess_read_history(offset?): retained history, not all future output; no cursor change. Page with
  offset=end. Neither historical read waits for more bytes.

End reasons: exec_finish (exec only), match, total_timeout, idle_timeout, aborted, cancelled,
output_limit, closed. Immediate reads need no reason when current data is exhausted. Total
clock starts at read invocation; idle starts then and resets on endpoint bytes. These read
conditions apply to read tools and stream-send collection, not sess_exec. Its total_timeout
is instead governed by exec_total_timeout_ms. Timeouts leave
connections open. end_pattern matches DECODED unread output, including bytes received before
this send; it does not match reason/error fields. Match consumes only through the match, leaving
later output unread. Never infer input/output pairing or successful execution from it.

Read output is valid UTF-8, without implicit ANSI stripping or EOL normalization. Supported
encodings: utf-8, gbk, shift-jis. Incomplete multibyte characters remain unread until completed;
closed streams/history finalize them under output_decode_error_action. Invalid bytes default
to visible \xNN; to_hex renders <NN>; error reports aborted and leaves the offending byte unread.
A decode error does not itself close transport. Override policy to inspect retained data.

max_output_bytes is 1..65536 UTF-8 display bytes, including escaped invalid-byte text. more=true
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
closure may happen earlier. A running SSH exec has a separate 110s no-output watchdog. If
connected=false, reconnect with sess_connect and inspect remote state before retrying side effects.
