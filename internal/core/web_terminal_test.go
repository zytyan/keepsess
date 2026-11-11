package core

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func TestHumanTerminalProtocolAndGuards(t *testing.T) {
	commands := make(chan string, 4)
	dimensions := make(chan [2]uint32, 4)
	addr, _ := testutil.SSHWithExec(t, func(command string, ch ssh.Channel, requests <-chan *ssh.Request) {
		commands <- command
		_, _ = io.WriteString(ch, "ready> ")
		go func() { _, _ = io.Copy(ch, ch) }()
		for req := range requests {
			switch req.Type {
			case "window-change":
				var size struct{ Width, Height, PixelsX, PixelsY uint32 }
				_ = ssh.Unmarshal(req.Payload, &size)
				dimensions <- [2]uint32{size.Width, size.Height}
			case "signal":
				var signal struct{ Name string }
				_ = ssh.Unmarshal(req.Payload, &signal)
				if signal.Name == "TERM" {
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
					return
				}
			}
		}
	})
	m := manager(t)
	events := &eventRecorder{}
	m.SetEventSink(events)
	id := connect(t, m, options(addr, "ssh", "exec"))
	v, err := m.WebOperate(context.Background(), "web_shell", Request{Session: id})
	if err != nil || v.Handle == "" || !v.Interactive || !v.PTY {
		t.Fatal(v, err)
	}
	select {
	case cmd := <-commands:
		if cmd != "exec bash -il" {
			t.Fatal(cmd)
		}
	case <-time.After(time.Second):
		t.Fatal("shell not started")
	}
	resized, err := m.WebOperate(context.Background(), "web_resize", Request{Handle: v.Handle, Columns: 132, Rows: 42})
	if err != nil || resized.InputState != "sent" {
		t.Fatal(resized, err)
	}
	select {
	case size := <-dimensions:
		if size != [2]uint32{132, 42} {
			t.Fatal(size)
		}
	case <-time.After(time.Second):
		t.Fatal("resize missing")
	}
	if _, err = m.Configure(id, cfg(map[string]any{"read_only": true})); err != nil {
		t.Fatal(err)
	}
	denied, err := m.WebOperate(context.Background(), "web_resize", Request{Handle: v.Handle, Columns: 100, Rows: 30})
	if err == nil || denied.InputState != "not_sent" {
		t.Fatal(denied, err)
	}
	denied, err = m.WebOperate(context.Background(), "web_shell", Request{Session: id})
	if err != nil || denied.InputState != "not_sent" || denied.Error == "" {
		t.Fatal(denied, err)
	}
	ended, err := m.WebOperate(context.Background(), "web_end", Request{Handle: v.Handle})
	if err != nil || ended.Signal.RequestState != "skipped_read_only" {
		t.Fatal(ended, err)
	}
	if _, err = m.Configure(id, cfg(map[string]any{"read_only": false})); err != nil {
		t.Fatal(err)
	}
	ended, err = m.WebOperate(context.Background(), "web_end", Request{Handle: v.Handle})
	if err != nil || ended.Signal.RequestState != "sent" {
		t.Fatal(ended, err)
	}
	done := run(t, m, "task_wait", Request{Handle: v.Handle})
	if done.State != "finished" {
		t.Fatal(done)
	}
	attributed := false
	for _, e := range events.snapshot() {
		if e.Kind == "output" && e.Handle == v.Handle {
			if e.Source != "mixed" || e.Origin != "human" {
				t.Fatal(e)
			}
			attributed = true
		}
	}
	if !attributed {
		t.Fatal("human output attribution missing")
	}
}

type blockedResizeChannel struct {
	*inputFakeChannel
	entered chan struct{}
	done    <-chan struct{}
}

func (c *blockedResizeChannel) WindowChange(int, int) error {
	close(c.entered)
	<-c.done
	return errors.New("transport closed")
}

func TestHumanResizeCancellationClosesSharedTransport(t *testing.T) {
	m, s, _ := localSession(t)
	s.mode = "exec"
	entered := make(chan struct{})
	s.newExec = func() (execChannel, error) {
		return &blockedResizeChannel{
			inputFakeChannel: &inputFakeChannel{fakeExecChannel: &fakeExecChannel{startFn: func(string, *Session) error { return nil }, waitFn: func() error { <-s.done; return errors.New("closed") }}, stdin: &blockedStdin{done: s.done, started: make(chan struct{})}},
			entered:          entered, done: s.done,
		}, nil
	}
	human, err := m.WebOperate(context.Background(), "web_shell", Request{Session: s.id})
	if err != nil || human.Handle == "" {
		t.Fatal(human, err)
	}
	other := run(t, m, "exec", Request{Session: s.id, Command: "other", CollectOutput: noCollect()})
	if other.Handle == "" {
		t.Fatal(other)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		v, _ := m.WebOperate(ctx, "web_resize", Request{Handle: human.Handle, Columns: 100, Rows: 30})
		done <- v
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resize did not start")
	}
	if _, err = m.Configure(s.id, cfg(map[string]any{"read_only": true})); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatal("resize must guard configuration", err)
	}
	cancel()
	select {
	case v := <-done:
		if v.InputState != "unknown" || !strings.Contains(v.Error, "shared transport closed") {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("resize cancellation did not finish")
	}
	for _, handle := range []string{human.Handle, other.Handle} {
		v := run(t, m, "task_status", Request{Handle: handle})
		if v.State != "unknown" || v.Connected {
			t.Fatal("shared task outcome must be unknown", v)
		}
	}
}
