---
name: sessmcp
description: Operate persistent SSH/Telnet sessions through sessmcp, including device consoles, incremental reads, input receipts and retry deduplication.
---

# sessmcp MCP operating skill

Configure an MCP stdio server with executable `sessmcp`, args `["serve"]`.
Audit: `["serve", "--log", "/private/path/audit.jsonl"]`. Authentication fields are redacted;
commands, line and data may still contain secrets.

## Choose the mode before sending commands

- Ordinary SSH server: `sess_connect` with interactive=false (default), then `sess_exec`.
  Supply protocol, address (host:port), SSH user, password/key_file and known_hosts/host_key.
  Use insecure=true only with explicit user intent to skip host-key verification.
  Each exec uses a new channel over the persistent transport; cwd/environment do not persist.
- Switch/router CLI, including Huawei-style consoles: connect with interactive=true, then
  `sess_interact`. Do not probe by executing the same command first in exec and then in a shell.
- Telnet is always interactive. Login by matching Username/Password prompts and sending one field
  at a time; never guess or automatically replay the device's authentication flow.

## Send one logical command, then read its result

Prefer `line` without newline characters for a single console command:

```json
{"session":"s1","line":"display version","input_id":"inspect-version-1","waitfor":"(?m)(<[^>\r\n]+>|\\[[^\r\n]*\\])\\s*$","wait":"30s"}
```

`line` appends the selected Enter exactly once: SSH defaults to CR, Telnet defaults to CRLF.
Set enter="cr"/"lf"/"crlf" only when the device requires it. Some consoles treat CR and LF as
separate Enters; CRLF can then trigger an empty-line replay. SSH devices should start with CR.
Do not add CR/LF to line; it rejects embedded line breaks. `line` and raw `data` are exclusive.
Use key="enter"/"ctrl_c"/"ctrl_d"/"ctrl_z"/"esc"/"tab" for control keys, exclusive with
line/data/data_escaped/enter. key=enter and line="" send SSH CR or Telnet CRLF. Use raw data for
pagination (a single space) or deliberate byte sequences;
raw data is never normalized or given an extra newline. JSON escapes must represent real control
characters, e.g. data="diagnose\r"; do not send literal backslash-r. JSON "\u0003" is Ctrl-C;
"\\r" and "&#13;" stay literal. JSON does not allow \xNN; no extra escape decoding occurs.

For visible escape text transported by an XML/JSON agent, prefer data_escaped. sessmcp decodes
one explicit escape layer AFTER JSON decoding, independent of the agent's presentation format.
Example: visible \r in the parameter is JSON {"data_escaped":"\\r"} and sends byte 0D;
visible \x03 is JSON {"data_escaped":"\\x03"} and sends Ctrl-C. It also supports \n, \t,
\\, escaped quotes, \uNNNN, \UNNNNNNNN and Go octal escapes. Ordinary quotes remain literal.
To send literal backslash-r, supply visible \\r (JSON {"data_escaped":"\\\\r"}).
Do not pre-decode repeatedly: decoding happens exactly once. Unknown/incomplete escapes reject
before any bytes are sent; HTML entities remain literal. data_escaped is exclusive with
line/data/key/enter. Input limits, receipts and deduplication use DECODED bytes. Auditing still
records input text, including secrets in data_escaped, just like raw data.

input_id is optional. When using it, assign one ID per logical command and keep it on retries. Within a
session's last 128 distinct IDs, the server rejects changed input under an old ID and suppresses
retransmission of the same input, even after an uncertain send. input_id is optional and distinct
from JSON-RPC request id. Deduplication does not survive eviction, reconnect or process restart.
Without input_id, two identical calls remain two intentional sends; do not repeat on empty output.

Read the receipt before deciding what to do next:

| input_state | Meaning | Action |
|---|---|---|
| sent | Transport accepted input; device completion is not implied | Read output, then sess_read if needed |
| not_sent | Busy or input size limit rejected this input before sending | Wait for the active call, or reduce input/configure max_input_bytes |
| unknown | Send failed/timed out and may have partially executed | Inspect remote state before another input |
| duplicate | Existing input_id was recognized; no new input was sent | Inspect output; poll sess_read |
| none | This call sent no input (read/list/connect) | input_seq refers to the latest session input |

input_seq identifies the send attempt; input_bytes is its intended byte count, not a confirmed
remote-execution count. When next_action="sess_read", call sess_read without command/line/data/key.
On busy with retry_same_input=true, wait for the active call (wait_current_call) or SSH command
to finish, then retry the same rejected input/ID. A not_sent ID was not registered.
Interactive running=true is advisory, not a requirement to read before sending another input.
**An empty output or reason=wait/timeout is never evidence that a command was not executed.**
For SSH exec, running=true means the original command is still active: poll, never resubmit.
If reason=exit_status_missing, the SSH channel closed without exit status. Preserve and inspect
its output; success is unknown. The transport is retained unless actually disconnected. For future
network-device commands use an interactive connection, without automatically replaying this command.

## Waits, prompts and history

wait is fixed time from invocation (default 5s), timeout is output silence, waitfor is Go RE2 regex.
These conditions and SSH completion are OR. Set wait explicitly when 5s is too short; duration
examples: 100ms, 30s, 1min. Calls are capped at 115s; active-operation output silence for 110s disconnects.
For an interactive call, the 110s clock starts at call start or last output, whichever is later.
SSH exec silence protection continues between polls; reads do not reset it.
Unused sessions default to 30min since the last operation; idle_timeout on connect changes this
lifetime, "0" disables reclamation. Long reasoning/approval without calls does not use the 110s
console deadline. Sending input does not refresh output silence; timeout cannot trigger before
its duration from the current call start.
A waitfor match is only as reliable as the pattern. Huawei-style prompts may contain ~ or * and
change between <host>, [~host], [*host] and nested views. Match an observed full prompt, not a guessed
mode name or arbitrary command echo. Device command names vary; use device help rather than guessing.
Polling matches unread output, including bytes arriving between calls; input calls match new output.
waitfor does not match tool reason/error fields; disconnects end waiting automatically.

Results are incremental and share one per-session cursor. For retained history use all=true,
tail=N, or offset=end to page. All three run tools accept max_output_bytes: default 8192,
maximum 65536, 0 uses default. History is at most 1MiB. Inspect more,
truncated and retained_from. Explicit all/tail/offset do not move the default cursor.

Session handles are short process-local IDs (s1, s2), never reused until restart.
Input defaults to 4096 bytes including Enter; set max_input_bytes on connect (0 uses default, up to 1MiB).
Use the handle actually returned by sess_connect; restart requires a new connection and handle.
After a complete Write, new input is permitted without output or a prompt. Write completion
means transport acceptance, not an empty TCP send queue, peer receipt, or remote execution.
There is no OS-specific TCP-full query: Write waits for capacity when needed.
TCP/SSH flow control blocks writes on Linux
and Windows; write timeout is at most 10s and bounded by wait from call start. Timeout or
cancellation during a write closes the connection with unknown input state. Concurrent
same-session calls return busy without queuing. Output backlog is reported independently.
Do not replay a sent/unknown input merely because output is empty.

SSH and Telnet explicitly enable TCP keepalive with a 15s period through the Go 1.22 API.
Actual probe/failure timing depends on the OS. Probes do not send console input, prove command
completion, or refresh the 110s endpoint output-silence clock.

Use sess_list to inspect connections, sess_close to release them or abort a blocked connection.
Close returns no terminal output and normal closure is not an error; read first if output matters.
CLI version and MCP initialize expose 0.2.0 plus build identity; check it when old behavior appears.
Cancellation stops waiting, not the remote command. On connected=false, reconnect and inspect state
before retrying side effects. Maximum 32 sessions and 32 tool workers. Across-session waits are
concurrent; same-session contention returns busy. A non-reading stdout consumer causes the server
to exit after 3s instead of deadlocking. Restarting requires new remote connections.
