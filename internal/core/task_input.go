package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

func (m *Manager) taskInput(ctx context.Context, kind string, r Request) (Result, error) {
	closeInput := kind == "task_close_stdin"
	switch kind {
	case "send_text", "send_escaped", "sendline_text", "sendline_escaped", "task_close_stdin":
	default:
		return Result{}, errors.New("unknown task input operation")
	}
	if r.Interactive || r.PTY || r.Command != "" || r.Signal != "" || r.Offset != nil || r.Lines != 0 {
		return Result{}, errors.New("unexpected task input parameter")
	}
	if closeInput && (r.Data != "" || r.CollectOutput != nil || len(r.Config) != 0) {
		return Result{}, errors.New("close_stdin only accepts handle")
	}
	t, err := m.acquireTask(r.Handle)
	if err != nil {
		return Result{}, err
	}
	defer t.session.touch(-1)
	defer t.out.touch(-1)
	s := t.session
	s.mu.Lock()
	c := s.config
	s.mu.Unlock()
	c, err = applyConfig(c, r.Config, true)
	if err != nil {
		return Result{}, err
	}
	if !t.inputMu.TryLock() {
		return Result{}, errors.New("busy: task stdin writer in progress; nothing sent")
	}
	locked := true
	defer func() {
		if locked {
			t.inputMu.Unlock()
		}
	}()
	v := t.out.status()
	v.InputState = "not_sent"
	// Check before acquiring the read lock, so a rejected input cannot consume output.
	if c.ReadOnly {
		v.Error = "read_only: task input disabled; nothing sent"
		return v, nil
	}
	collect := !closeInput && (r.CollectOutput == nil || *r.CollectOutput)
	if collect {
		if !t.out.readMu.TryLock() {
			return Result{}, errors.New("busy: another reader owns this task; nothing sent")
		}
		defer t.out.readMu.Unlock()
	}
	var b []byte
	if !closeInput {
		b, err = encodeInput(r.Data, strings.HasSuffix(kind, "_escaped"), c.Encoding)
		if err != nil {
			return Result{}, err
		}
		if strings.HasPrefix(kind, "sendline_") {
			end, _ := encodeInput(c.LineEnd, true, "utf-8")
			b = append(b, end...)
		}
		if int64(len(b)) > c.MaxInputBytes {
			return Result{}, fmt.Errorf("input exceeds max_input_bytes=%d; nothing sent", c.MaxInputBytes)
		}
	}
	if !s.writeMu.TryRLock() {
		return Result{}, errors.New("busy: configure/reconnect in progress; nothing sent")
	}
	controlLocked := true
	defer func() {
		if controlLocked {
			s.writeMu.RUnlock()
		}
	}()
	s.mu.Lock()
	guard, unavailable := s.config.ReadOnly, s.disposed || s.reconnecting || !s.connected
	s.mu.Unlock()
	v = t.out.status()
	v.InputState = "not_sent"
	v.InputBytes = len(b)
	if guard {
		v.Error = "read_only: task input disabled; nothing sent"
		return v, nil
	}
	if unavailable {
		v.Error = "connection unavailable; nothing sent"
		return v, nil
	}
	t.out.mu.Lock()
	interactive, state, open := t.out.interactive, t.out.state, t.out.stdinOpen
	stdin := t.stdin
	t.out.mu.Unlock()
	if !interactive {
		v.Error = "stdin unavailable: task was started without interactive=true; cannot enable it after startup"
		return v, nil
	}
	if state == "starting" {
		v.Error = "busy: task stdin setup in progress; nothing sent"
		return v, nil
	}
	if !open && closeInput {
		v.Warning = "stdin already closed; no EOF request sent"
		return v, nil
	}
	if !open || state != "running" || stdin == nil || !v.Connected {
		v.Error = "task stdin unavailable, closed or task not running; nothing sent"
		return v, nil
	}
	if c.BackpressureBytes > 0 && !closeInput {
		if s.tcp == nil {
			v.Error = "TCP queue query unavailable"
			return v, nil
		}
		n, e := queuedBytes(s.tcp)
		if e != nil {
			v.Error = e.Error()
			return v, nil
		}
		v.QueuedBytes = &n
		if int64(n) >= c.BackpressureBytes {
			v.Error = "backpressure: TCP queue at threshold; nothing sent"
			return v, nil
		}
	}
	if ctx.Err() != nil {
		v.Reason = "cancelled"
		return v, nil
	}
	type receipt struct {
		n   int
		err error
	}
	done := make(chan receipt, 1)
	go func() {
		if closeInput {
			done <- receipt{err: stdin.Close()}
			return
		}
		n, e := stdin.Write(b)
		if n != len(b) && e == nil {
			e = io.ErrShortWrite
		}
		done <- receipt{n, e}
	}()
	timer := time.NewTimer(c.Duration(c.WriteTimeoutMS))
	defer timer.Stop()
	var result receipt
	select {
	case result = <-done:
	case <-timer.C:
		result.err = errors.New("task stdin write timeout; shared transport closed; input may have executed")
		s.disconnectWith("transport_timeout", result.err.Error())
	case <-ctx.Done():
		result.err = ctx.Err()
		s.disconnectError(result.err, "task stdin write cancelled; shared transport closed")
	}
	if closeInput {
		t.out.mu.Lock()
		t.out.stdinOpen = false
		t.out.mu.Unlock()
	}
	v = t.out.status()
	v.InputState = "sent"
	v.InputBytes = len(b)
	v.WrittenBytes = result.n
	if result.err != nil {
		t.out.mu.Lock()
		t.out.stdinOpen = false
		t.out.mu.Unlock()
		v = t.out.status()
		v.InputState = "unknown"
		v.InputBytes = len(b)
		v.WrittenBytes = result.n
		v.Error = result.err.Error()
		v.Reason = "aborted"
		if errors.Is(result.err, context.Canceled) {
			v.Reason = "cancelled"
		}
		return v, nil
	}
	s.writeMu.RUnlock()
	controlLocked = false
	t.inputMu.Unlock()
	locked = false
	if !collect {
		return v, nil
	}
	output := t.out.read(ctx, "read_until", Request{}, c)
	output.InputState, output.InputBytes, output.WrittenBytes = v.InputState, v.InputBytes, v.WrittenBytes
	return output, nil
}
