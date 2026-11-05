package core

import (
	"context"
	"errors"
	"regexp"
	"time"
)

func (s *Session) read(ctx context.Context, kind string, r Request, c Config) Result {
	started := time.Now()
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
		if rawEnd-start > 2*c.MaxOutputBytes+4 {
			rawEnd = start + 2*c.MaxOutputBytes + 4
		}
		raw := append([]byte(nil), s.buf[start-base:rawEnd-base]...)
		v := s.statusLocked()
		s.mu.Unlock()
		if disposed {
			return Result{Session: s.id, Handle: s.handle, Reason: "closed", Connected: false, Comment: "Connection/task was closed; retained output has been discarded. Remote outcome may be unknown."}
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
		d := decodeWindow(raw, c, final && rawEnd == total)
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
				s.mu.Unlock()
				return Result{Session: s.id, Handle: s.handle, Reason: "closed", Connected: false, Comment: "Connection/task was closed; retained output has been discarded. Remote outcome may be unknown."}
			}
			// If the producer overtook the snapshot, report that loss on the next read.
			if !history {
				s.cursor = end
			}
			v.Connected = s.connected
			v.Running = s.running
			v.State, v.ExitCode, v.ExitSignal, v.Warning = s.state, s.exit, s.exitSignal, s.warning
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
				case v.Error != "":
					v.Comment = "No output returned because the operation encountered an error; inspect error and state before continuing."
				case !v.Connected || v.Reason == "closed":
					v.Comment = "No output available from the closed connection. Empty output does not establish the remote command outcome."
				case v.Reason == "cancelled":
					v.Comment = "Output collection was cancelled without returning text. This does not imply the command stopped or was not executed."
				case d.limited:
					v.Comment = "Output bytes are available, but max_output_bytes is too small to return the next complete character or byte escape."
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
