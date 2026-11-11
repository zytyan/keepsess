package core

import (
	"context"
	"errors"
	"time"
)

type webTerminalKey struct{}

// WebCapabilities is an additive daemon contract, independent of MCP discovery.
func (m *Manager) WebCapabilities() []string {
	return []string{"web_shell.v1", "web_resize.v1", "web_end.v1"}
}

// WebOperate never consumes output, reconnects a session or replays application input.
func (m *Manager) WebOperate(ctx context.Context, kind string, r Request) (Result, error) {
	if kind == "web_shell" {
		no := false
		return m.Operate(context.WithValue(ctx, webTerminalKey{}, true), "exec", Request{Session: r.Session, Command: "exec bash -il", Login: &no, Interactive: true, PTY: true, CollectOutput: &no})
	}
	t, err := m.acquireTask(r.Handle)
	if err != nil {
		return Result{InputState: "not_sent"}, err
	}
	defer t.session.touch(-1)
	defer t.out.touch(-1)
	t.out.mu.Lock()
	human, pty, ch, running := t.out.origin == "human", t.out.pty, t.channel, t.out.state == "running"
	t.out.mu.Unlock()
	if !human {
		return Result{InputState: "not_sent"}, errors.New("not a human terminal; nothing sent")
	}
	if kind == "web_end" {
		signal := t.sendSignal(ctx, "TERM")
		v := t.out.status()
		v.Signal = signal
		return v, nil
	}
	if kind != "web_resize" || r.Columns < 10 || r.Columns > 1000 || r.Rows < 3 || r.Rows > 500 {
		return Result{InputState: "not_sent"}, errors.New("invalid terminal dimensions or operation")
	}
	resize, ok := ch.(interface{ WindowChange(int, int) error })
	if !ok || !pty || !running {
		return Result{InputState: "not_sent"}, errors.New("PTY unavailable")
	}
	if !t.signalMu.TryLock() {
		return Result{InputState: "not_sent"}, errors.New("busy: terminal control write")
	}
	defer t.signalMu.Unlock()
	s := t.session
	if !s.writeMu.TryRLock() {
		return Result{InputState: "not_sent"}, errors.New("busy: configure or reconnect")
	}
	defer s.writeMu.RUnlock()
	s.mu.Lock()
	guard, unavailable := s.config.ReadOnly, !s.connected || s.disposed || s.reconnecting
	s.mu.Unlock()
	if guard || unavailable {
		return Result{InputState: "not_sent"}, errors.New("read_only or disconnected; nothing sent")
	}
	if ctx.Err() != nil {
		return Result{InputState: "not_sent"}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- resize.WindowChange(r.Rows, r.Columns) }()
	timer := time.NewTimer(SignalLimit)
	defer timer.Stop()
	v := Result{Handle: r.Handle, InputState: "sent"}
	select {
	case err = <-done:
		if err != nil {
			v.InputState = "unknown"
			v.Error = err.Error()
		}
	case <-timer.C:
		v.InputState = "unknown"
		v.Error = "PTY resize timeout; shared transport closed; task outcomes unknown"
		s.disconnect(v.Error)
	case <-ctx.Done():
		v.InputState = "unknown"
		v.Error = "PTY resize cancelled; shared transport closed; task outcomes unknown"
		s.disconnect(v.Error)
	}
	return v, nil
}
