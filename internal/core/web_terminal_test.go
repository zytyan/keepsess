package core

import (
	"bufio"
	"context"
	"encoding/binary"
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

func TestHumanPTYEchoAndMCPModes(t *testing.T) {
	type mode struct {
		terminal string
		echo     uint32
	}
	seen := make(chan mode, 4)
	addr, _ := testutil.SSHWithExecPTY(t, func(_ string, ch ssh.Channel, _ <-chan *ssh.Request) { _, _ = io.Copy(ch, ch) }, func(req *ssh.Request) {
		var p struct {
			Terminal                        string
			Width, Height, PixelsX, PixelsY uint32
			Modes                           string
		}
		if ssh.Unmarshal(req.Payload, &p) != nil {
			return
		}
		var echo uint32
		b := []byte(p.Modes)
		for len(b) >= 5 && b[0] != 0 {
			if b[0] == ssh.ECHO {
				echo = binary.BigEndian.Uint32(b[1:5])
			}
			b = b[5:]
		}
		seen <- mode{p.Terminal, echo}
	})
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	human, err := m.WebOperate(context.Background(), "web_shell", Request{Session: id})
	if err != nil || human.Handle == "" {
		t.Fatal(human, err)
	}
	select {
	case got := <-seen:
		if got.terminal != "xterm-256color" || got.echo != 1 {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("human PTY request missing")
	}
	normal := run(t, m, "exec", Request{Session: id, Command: "interactive", Interactive: true, PTY: true, CollectOutput: noCollect()})
	if normal.Handle == "" {
		t.Fatal(normal)
	}
	select {
	case got := <-seen:
		if got.terminal != "vt100" || got.echo != 0 {
			t.Fatal("MCP PTY defaults changed", got)
		}
	case <-time.After(time.Second):
		t.Fatal("MCP PTY request missing")
	}
}

// A REPL that ignores EOF can print prompts forever under non-interactive exec.
// Interactive stdin must remain open and reads must never send terminal replies.
func TestREPLStdinEOFVersusInteractive(t *testing.T) {
	addr, _ := testutil.SSHWithExec(t, func(_ string, ch ssh.Channel, _ <-chan *ssh.Request) {
		reader := bufio.NewReader(ch)
		for i := 0; i < 8; i++ {
			_, _ = io.WriteString(ch, "\x1b[6nquery> ")
			line, err := reader.ReadString('\n')
			if err != nil {
				continue
			}
			_, _ = io.WriteString(ch, "received:"+line)
		}
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
	})
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	config := cfg(map[string]any{"read_total_timeout_ms": 1000, "read_idle_timeout_ms": 0, "end_pattern": "query> "})
	job := run(t, m, "exec", Request{Session: id, Command: "repl", Interactive: true, PTY: true, CollectOutput: noCollect()})
	first := run(t, m, "read_until", Request{Handle: job.Handle, Config: config})
	if strings.Count(first.Output, "query> ") != 1 {
		t.Fatal(first)
	}
	quiet := run(t, m, "read_until", Request{Handle: job.Handle, Config: cfg(map[string]any{"read_total_timeout_ms": 100, "read_idle_timeout_ms": 50})})
	if quiet.Output != "" {
		t.Fatal("reading or parser replies injected stdin", quiet)
	}
	sent := run(t, m, "send_text", Request{Handle: job.Handle, Data: "select 1;\n", Config: config})
	if sent.InputState != "sent" || strings.Count(sent.Output, "received:select 1;") != 1 || strings.Count(sent.Output, "query> ") != 1 {
		t.Fatal(sent)
	}
	run(t, m, "task_close_stdin", Request{Handle: job.Handle})
	defaultExec := run(t, m, "exec", Request{Session: id, Command: "repl"})
	if strings.Count(defaultExec.Output, "query> ") != 8 {
		t.Fatal("non-interactive stdin must retain EOF semantics", defaultExec)
	}
}
