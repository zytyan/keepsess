package core

import (
	"context"
	"errors"
	"regexp"
	"time"
)

func (s *Session) read(ctx context.Context, kind string, r Request, c Config) Result {
	started := time.Now()
	viewActive := c.StripANSI || c.IncludePattern != "" || c.ExcludePattern != ""
	totalTimeoutMS := c.ReadTotalTimeoutMS
	if kind == "exec_wait" {
		// SSH exec collection must not end because
		// a terminal goes quiet or happens to print a configured prompt.
		totalTimeoutMS = c.ExecTotalTimeoutMS
	}
	deadline := started.Add(c.Duration(totalTimeoutMS))
	var re *regexp.Regexp
	if kind == "read_until" && c.EndPattern != "" {
		re = regexp.MustCompile(c.EndPattern)
	}
	history := kind == "read_history" || kind == "read_tail"
	s.mu.Lock()
	from := s.cursor
	if kind == "read_history" {
		from = 0
		if r.Offset != nil {
			from = *r.Offset
		}
	}
	if kind == "read_tail" {
		lines := r.Lines
		if lines == 0 {
			lines = 20
		}
		pos := len(s.buf)
		if pos > 0 && s.buf[pos-1] == '\n' {
			pos--
		}
		for pos > 0 {
			pos--
			if s.buf[pos] == '\n' {
				lines--
				if lines == 0 {
					pos++
					break
				}
			}
		}
		from = s.base + int64(pos)
		if pos == 0 && s.base > 0 {
			from = 0
		}
	}
	s.mu.Unlock()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		base, total, last, connected, run, completed, disposed, failure := s.base, s.total, s.last, s.connected, s.running, s.completed, s.disposed, s.failure
		start := from
		dropped := int64(0)
		if start < base {
			dropped = base - start
			start = base
		}
		if start > total {
			start = total
		}
		// Decode only a bounded window, not the entire retained history.
		rawEnd := total
		if viewActive && rawEnd-start > ViewScanLimit {
			rawEnd = start + ViewScanLimit
		}
		if !viewActive && rawEnd-start > 2*c.MaxOutputBytes+4 {
			rawEnd = start + 2*c.MaxOutputBytes + 4
		}
		raw := append([]byte(nil), s.buf[start-base:rawEnd-base]...)
		v := s.statusLocked()
		s.mu.Unlock()
		if disposed {
			return Result{Session: s.id, Handle: s.handle, Reason: "closed", Connected: false, DisconnectReason: v.DisconnectReason, NextAction: "sess_connect", Comment: "Connection/task was closed; retained output has been discarded. Remote outcome may be unknown."}
		}
		now := time.Now()
		idleSince := last
		if idleSince.Before(started) {
			idleSince = started
		}
		idleReached := kind == "read_until" && c.ReadIdleTimeoutMS > 0 && now.Sub(idleSince) >= c.Duration(c.ReadIdleTimeoutMS)
		totalReached := !now.Before(deadline)
		immediate := kind != "read_until" && kind != "exec_wait"
		// A live incomplete character survives idle/immediate reads; only closed,
		// completed exec or an explicit history read finalizes its invalid bytes.
		final := !connected || (s.mode == "exec" && completed) || history
		decodeConfig := c
		if viewActive {
			decodeConfig.MaxOutputBytes = 4*ViewScanLimit + 4
		}
		d := decodeWindow(raw, decodeConfig, final && rawEnd == total)
		reason := ""
		output := d.text
		consumed := d.consumed
		if re != nil {
			if loc := re.FindStringIndex(output); loc != nil {
				end := loc[1]
				consumed = d.bounds[end]
				// Do not split a rendered byte escape or Unicode character.
				for end < len(output) && d.bounds[end+1] == consumed {
					end++
				}
				output = output[:end]
				reason = "match"
			}
		}
		if viewActive {
			d.text, d.consumed = output, consumed
			d.bounds = d.bounds[:len(output)+1]
			view := projectOutput(d, c, final && rawEnd == total || reason == "match" || totalReached || idleReached || ctx.Err() != nil, (!connected || (s.mode == "exec" && completed)) && rawEnd == total)
			output, consumed = view.text, view.consumed
			v.FilteredBytes, v.FilteredLines, v.StrippedBytes, v.LineFragments = view.filteredBytes, view.filteredLines, view.strippedBytes, view.fragments
			d.limited, d.minimum, d.incomplete = view.limited, view.minimum, view.incomplete
			if reason == "match" && view.incomplete {
				reason = "view_incomplete"
			}
			if view.limited {
				reason = "output_limit"
			} else if len(raw) >= ViewScanLimit && (rawEnd < total || !final) && reason == "" {
				reason = "scan_limit"
				if consumed == 0 {
					reason = "view_incomplete"
				}
			}
		}
		if reason == "" {
			switch {
			case failure != "" && v.State == "unknown":
				reason = "aborted"
			case d.err != nil:
				reason = "aborted"
				v.Error = d.err.Error()
			case d.limited || int64(len(output)) >= c.MaxOutputBytes:
				reason = "output_limit"
			case ctx.Err() != nil:
				reason = "cancelled"
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					reason = "total_timeout"
				}
			case !connected:
				reason = "closed"
				if failure != "" {
					reason = "aborted"
				}
			case s.mode == "exec" && completed && !run:
				reason = "exec_finish"
			case immediate:
				if rawEnd < total {
					reason = "output_limit"
				}
			case totalReached:
				reason = "total_timeout"
			case idleReached:
				reason = "idle_timeout"
			}
		}
		if reason != "" || immediate {
			end := start + int64(consumed)
			elapsed := time.Since(started).Milliseconds()
			v.WaitElapsedMS = &elapsed
			if reason == "total_timeout" {
				v.TimeoutSource = "read_total"
				if kind == "exec_wait" {
					v.TimeoutSource = "exec_wait"
				}
				if ctx.Err() != nil {
					v.TimeoutSource = "caller_deadline"
					if rpc, ok := ctx.Value(callDeadlineKey{}).(time.Time); ok && !time.Now().Before(rpc) {
						v.TimeoutSource = "rpc_budget"
					}
				}
			}
			v.Output = output
			v.Reason = reason
			v.Start = start
			v.End = end
			v.More = end < total
			v.DroppedBytes = dropped
			if len(output) == 0 && d.limited {
				v.MinimumOutputBytes = d.minimum
			}
			s.mu.Lock()
			if s.disposed {
				v = s.statusLocked()
				s.mu.Unlock()
				return Result{Session: s.id, Handle: s.handle, Reason: "closed", Connected: false, DisconnectReason: v.DisconnectReason, NextAction: "sess_connect", Comment: "Connection/task was closed; retained output has been discarded. Remote outcome may be unknown."}
			}
			// If the producer overtook the snapshot, report that loss on the next read.
			if !history {
				s.cursor = end
			}
			v.DisconnectReason = s.disconnectReason
			v.Connected = s.connected
			v.Running = s.running
			v.State, v.ExitCode, v.ExitSignal, v.Warning = s.state, s.exit, s.exitSignal, s.warning
			if !s.startedAt.IsZero() {
				end := time.Now()
				if !s.finishedAt.IsZero() {
					end = s.finishedAt
				}
				v.TaskElapsedMS = end.Sub(s.startedAt).Milliseconds()
			}
			v.More = end < s.total
			if v.Error == "" {
				v.Error = s.failure
			}
			if !s.connected {
				v.NextAction = "sess_reconnect"
			}
			s.mu.Unlock()
			if v.Output == "" {
				switch {
				case v.Reason == "view_incomplete":
					v.Comment = "Output projection encountered an incomplete ANSI sequence at a bounded scan/pattern boundary; raw history is preserved. Disable strip_ansi/filters to inspect raw output."
				case v.FilteredLines > 0 || v.StrippedBytes > 0:
					v.Comment = "New bytes were scanned but produced no visible output after ANSI stripping/line filtering; inspect counters and raw offsets. Raw history is retained subject to normal retention limits."
				case v.Error != "":
					v.Comment = "No output returned because the operation encountered an error; inspect error and state before continuing."
				case !v.Connected || v.Reason == "closed":
					v.Comment = "No output available from the closed connection. Empty output does not establish the remote command outcome."
				case v.Reason == "cancelled":
					v.Comment = "Output collection was cancelled without returning text. This does not imply the command stopped or was not executed."
				case d.limited:
					v.Comment = "Output bytes are available, but max_output_bytes is too small to return the next complete character or byte escape."
				case viewActive && len(raw) > 0 && consumed == 0:
					v.Comment = "New bytes are retained, but output projection needs a complete line/ANSI sequence/character; try a bounded read_until or disable projection to inspect raw history."
				case len(raw) > 0 && consumed == 0:
					v.Comment = "The call is valid. Partial encoded bytes were received; no complete text is available yet."
				case v.Reason == "exec_finish":
					v.Comment = "The command channel finished. This read returned no output; inspect exit_code, exit_signal and warning for its outcome."
				case history:
					v.Comment = "The call is valid. No retained output is available in the requested range; inspect dropped_bytes for history loss."
				case v.Reason == "total_timeout" || v.Reason == "idle_timeout":
					v.Comment = "The call is valid. No returnable output was observed during this wait. Do not resend input or add Enter solely because output is empty."
				default:
					v.Comment = "The call is valid. No new returnable output is available; earlier output may already have been consumed. Empty output does not establish execution or authentication success."
				}
			}
			return v
		}
		select {
		case <-ctx.Done():
		case <-s.notify:
		case <-ticker.C:
		}
	}
}
