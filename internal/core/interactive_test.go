package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func inputServer(t *testing.T) string {
	addr, _ := testutil.SSHWithExec(t, func(command string, ch ssh.Channel, reqs <-chan *ssh.Request) {
		if command == "default-eof" {
			b, _ := io.ReadAll(ch)
			_, _ = io.WriteString(ch, "eof:"+string(b))
		} else {
			_, _ = io.WriteString(ch, "ready> ")
			_, _ = io.Copy(ch, ch)
		}
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
	})
	return addr
}
func TestInteractiveExecInputAndEOF(t *testing.T) {
	m := manager(t)
	id := connect(t, m, options(inputServer(t), "ssh", "exec"))
	v := run(t, m, "exec", Request{Session: id, Command: "default-eof"})
	if v.Reason != "exec_finish" || v.Output != "eof:" || v.Handle != "" {
		t.Fatal(v)
	}
	a := run(t, m, "exec", Request{Session: id, Command: "echo", Interactive: true, CollectOutput: noCollect()})
	if a.Handle == "" || !a.Interactive || a.StdinOpen == nil || !*a.StdinOpen || a.Comment != "" {
		t.Fatal(a)
	}
	run(t, m, "read_until", Request{Handle: a.Handle, Config: cfg(map[string]any{"end_pattern": "ready> "})})
	for _, tc := range []struct{ kind, data, want string }{
		{"send_text", `\x03`, `\x03`},
		{"send_escaped", `\x03`, "\x03"},
		{"sendline_text", "hello", "hello\r"},
		{"sendline_escaped", `a\x00`, "a\x00\r"},
	} {
		v = run(t, m, tc.kind, Request{Handle: a.Handle, Data: tc.data, Config: cfg(map[string]any{"end_pattern": "", "read_idle_timeout_ms": 10, "output_decode_error_action": "to_escape_bytes"})})
		if v.InputState != "sent" || v.WrittenBytes != len(tc.want) || v.Output != tc.want {
			t.Fatal(tc.kind, v)
		}
	}
	// A Ctrl-D byte is data when there is no PTY, not stdin EOF.
	v = run(t, m, "send_escaped", Request{Handle: a.Handle, Data: `\x04`, Config: cfg(map[string]any{"read_idle_timeout_ms": 10})})
	if v.Output != "\x04" || v.StdinOpen == nil || !*v.StdinOpen {
		t.Fatal(v)
	}
	v = run(t, m, "task_close_stdin", Request{Handle: a.Handle})
	if v.InputState != "sent" || v.StdinOpen == nil || *v.StdinOpen || v.Output != "" {
		t.Fatal(v)
	}
	v = run(t, m, "task_wait", Request{Handle: a.Handle})
	if v.Reason != "exec_finish" || v.ExitCode == nil || *v.ExitCode != 0 {
		t.Fatal(v)
	}
	v = run(t, m, "task_close_stdin", Request{Handle: a.Handle})
	if v.InputState != "not_sent" || v.Warning == "" {
		t.Fatal(v)
	}
	v = run(t, m, "send_text", Request{Handle: a.Handle, Data: "late"})
	if v.InputState != "not_sent" || v.Error == "" {
		t.Fatal(v)
	}
}

func TestInteractiveGuardVariantsAndPTY(t *testing.T) {
	m := manager(t)
	id := connect(t, m, options(inputServer(t), "ssh", "exec"))
	if _, err := m.Operate(context.Background(), "exec", Request{Session: id, Command: "echo", PTY: true}); err == nil {
		t.Fatal("pty without interactive accepted")
	}
	a := run(t, m, "exec", Request{Session: id, Command: "echo", Interactive: true, PTY: true, CollectOutput: noCollect()})
	if !a.PTY || !a.Interactive || a.InputState != "sent" {
		t.Fatal(a)
	}
	if _, err := m.Configure(id, cfg(map[string]any{"read_only": true})); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"send_text", "send_escaped", "sendline_text", "sendline_escaped", "task_close_stdin"} {
		v := run(t, m, kind, Request{Handle: a.Handle})
		if v.InputState != "not_sent" || !strings.Contains(v.Error, "read_only") {
			t.Fatal(kind, v)
		}
	}
	if _, err := m.Configure(id, cfg(map[string]any{"read_only": false})); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Request{{Session: id, Handle: a.Handle, Data: "x"}, {Handle: a.Handle, Data: `\q`}, {Handle: a.Handle, Data: strings.Repeat("x", 4097)}, {Handle: a.Handle, Data: "x", Config: cfg(map[string]any{"read_only": false})}} {
		if _, err := m.Operate(context.Background(), "send_escaped", r); err == nil {
			t.Fatal("invalid input accepted", r)
		}
	}
	v := run(t, m, "task_close_stdin", Request{Handle: a.Handle})
	if v.InputState != "sent" {
		t.Fatal(v)
	}
	v = run(t, m, "task_wait", Request{Handle: a.Handle})
	if v.Reason != "exec_finish" {
		t.Fatal(v)
	}
	ordinary := run(t, m, "exec", Request{Session: id, Command: "default-eof", CollectOutput: noCollect()})
	v = run(t, m, "send_text", Request{Handle: ordinary.Handle, Data: "no"})
	if v.InputState != "not_sent" || !strings.Contains(v.Error, "interactive=true") {
		t.Fatal(v)
	}
}

func TestTaskInputWhileSyncCollects(t *testing.T) {
	m := manager(t)
	id := connect(t, m, options(inputServer(t), "ssh", "exec"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		v, _ := m.Operate(ctx, "exec", Request{Session: id, Command: "echo", Interactive: true})
		done <- v
	}()
	var handle string
	eventually(t, func() bool {
		list, _ := m.TaskList(id)
		if len(list) == 1 && list[0].State == "running" {
			handle = list[0].Handle
			return true
		}
		return false
	})
	v := run(t, m, "send_text", Request{Handle: handle, Data: "duplex", CollectOutput: noCollect()})
	if v.InputState != "sent" || v.Comment != "" {
		t.Fatal(v)
	}
	v = run(t, m, "task_close_stdin", Request{Handle: handle})
	if v.InputState != "sent" {
		t.Fatal(v)
	}
	select {
	case v = <-done:
		if v.Reason != "exec_finish" || !strings.Contains(v.Output, "duplex") {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("sync collector failed")
	}
}

type blockedStdin struct {
	done    <-chan struct{}
	started chan struct{}
	once    sync.Once
}

func (b *blockedStdin) Write(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.done
	return 0, errors.New("closed")
}
func (b *blockedStdin) Close() error { return nil }

type inputFakeChannel struct {
	*fakeExecChannel
	stdin io.WriteCloser
}

func (f *inputFakeChannel) prepareInput(bool) (io.WriteCloser, error) { return f.stdin, nil }

func TestTaskInputWriteGuardAndTimeout(t *testing.T) {
	m, s, _ := localSession(t)
	s.mode = "exec"
	input := &blockedStdin{done: s.done, started: make(chan struct{})}
	s.newExec = func() (execChannel, error) {
		return &inputFakeChannel{&fakeExecChannel{startFn: func(string, *Session) error { return nil }, waitFn: func() error { <-s.done; return errors.New("closed") }, signalFn: func(ssh.Signal) error { return nil }}, input}, nil
	}
	a := run(t, m, "exec", Request{Session: s.id, Command: "blocked", Interactive: true, CollectOutput: noCollect()})
	done := make(chan Result, 1)
	go func() {
		v, _ := m.Operate(context.Background(), "send_text", Request{Handle: a.Handle, Data: "x", CollectOutput: noCollect(), Config: cfg(map[string]any{"write_timeout_ms": 100})})
		done <- v
	}()
	select {
	case <-input.started:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	if _, err := m.Configure(s.id, cfg(map[string]any{"read_only": true})); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatal("guard bypassed writer", err)
	}
	if _, err := m.Operate(context.Background(), "task_close_stdin", Request{Handle: a.Handle}); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatal("parallel stdin writer", err)
	}
	select {
	case v := <-done:
		if v.InputState != "unknown" || v.Connected || v.DisconnectReason != "transport_timeout" {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("write not bounded")
	}
}

type rejectedPTYChannel struct{ *fakeExecChannel }

func (r *rejectedPTYChannel) prepareInput(bool) (io.WriteCloser, error) {
	return nil, errors.New("PTY rejected")
}
func TestInteractiveSetupFailureDoesNotLaunch(t *testing.T) {
	m, s, _ := localSession(t)
	s.mode = "exec"
	s.newExec = func() (execChannel, error) {
		return &rejectedPTYChannel{&fakeExecChannel{startFn: func(string, *Session) error { t.Error("command launched after rejected PTY"); return nil }, waitFn: func() error { return nil }, signalFn: func(ssh.Signal) error { return nil }}}, nil
	}
	v := run(t, m, "exec", Request{Session: s.id, Command: "must-not-run", Interactive: true, PTY: true, CollectOutput: noCollect()})
	if v.InputState != "not_sent" || v.Handle != "" || v.Error == "" || !v.Connected {
		t.Fatal(v)
	}
}

type blockedEOF struct {
	done    <-chan struct{}
	started chan struct{}
}

func (b *blockedEOF) Write(p []byte) (int, error) { return len(p), nil }
func (b *blockedEOF) Close() error                { close(b.started); <-b.done; return errors.New("closed") }
func TestInteractiveEOFDeadlineAndCancellation(t *testing.T) {
	for _, cancelWrite := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelWrite), func(t *testing.T) {
			m, s, _ := localSession(t)
			s.mode = "exec"
			input := &blockedEOF{done: s.done, started: make(chan struct{})}
			s.newExec = func() (execChannel, error) {
				return &inputFakeChannel{&fakeExecChannel{startFn: func(string, *Session) error { return nil }, waitFn: func() error { <-s.done; return errors.New("closed") }, signalFn: func(ssh.Signal) error { return nil }}, input}, nil
			}
			a := run(t, m, "exec", Request{Session: s.id, Command: "blocked", Interactive: true, CollectOutput: noCollect()})
			if _, err := m.Configure(s.id, cfg(map[string]any{"write_timeout_ms": 100})); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan Result, 1)
			go func() { v, _ := m.Operate(ctx, "task_close_stdin", Request{Handle: a.Handle}); done <- v }()
			select {
			case <-input.started:
			case <-time.After(time.Second):
				t.Fatal("EOF did not start")
			}
			if cancelWrite {
				cancel()
			}
			select {
			case v := <-done:
				if v.InputState != "unknown" || v.Connected || v.StdinOpen == nil || *v.StdinOpen {
					t.Fatal(v)
				}
				if cancelWrite && v.Reason != "cancelled" {
					t.Fatal(v)
				}
			case <-time.After(time.Second):
				t.Fatal("EOF not bounded")
			}
		})
	}
}
