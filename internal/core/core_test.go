package core

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/sessmcp/internal/testutil"
)

func manager(t *testing.T) *Manager { t.Helper(); m := NewManager(); t.Cleanup(m.CloseAll); return m }
func run(t *testing.T, m *Manager, kind string, r Request) Result {
	t.Helper()
	v, e := m.Run(context.Background(), kind, r)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestSSHExitAndIncremental(t *testing.T) {
	addr, key := testutil.SSH(t)
	m := manager(t)
	v, e := m.Connect(context.Background(), Options{Protocol: "ssh", Address: addr, User: "test", Password: "test", HostKey: key})
	if e != nil {
		t.Fatal(e)
	}
	id := v.Session
	start := time.Now()
	v = run(t, m, "exec", Request{Session: id, Command: "both", Wait: "30s"})
	if v.Reason != "exit" || v.ExitCode == nil || *v.ExitCode != 0 || !strings.Contains(v.Output, "stdout") || !strings.Contains(v.Output, "stderr") || time.Since(start) > time.Second {
		t.Fatalf("SSH completion: %+v", v)
	}
	v = run(t, m, "exec", Request{Session: id, Command: "exit7"})
	if v.ExitCode == nil || *v.ExitCode != 7 {
		t.Fatalf("exit status: %+v", v)
	}
	v = run(t, m, "exec", Request{Session: id, Command: "slow", Wait: "30ms"})
	if !v.Running || !strings.Contains(v.Output, "first") {
		t.Fatalf("partial: %+v", v)
	}
	busy := run(t, m, "exec", Request{Session: id, Command: "both"})
	if busy.Reason != "busy" {
		t.Fatalf("expected busy: %+v", busy)
	}
	v = run(t, m, "read", Request{Session: id, Wait: "1s"})
	if v.Running || !strings.Contains(v.Output, "second") || strings.Contains(v.Output, "first") {
		t.Fatalf("delta: %+v", v)
	}
	v = run(t, m, "read", Request{Session: id, All: true})
	if !strings.Contains(v.Output, "first") || !strings.Contains(v.Output, "second") {
		t.Fatalf("history: %+v", v)
	}
}
func TestHostVerification(t *testing.T) {
	addr, _ := testutil.SSH(t)
	m := manager(t)
	_, e := m.Connect(context.Background(), Options{Protocol: "ssh", Address: addr, User: "x", Password: "x", HostKey: "SHA256:bad"})
	if e == nil {
		t.Fatal("host key mismatch accepted")
	}
}
func TestDeviceSimulators(t *testing.T) {
	for _, protocol := range []string{"ssh", "telnet"} {
		t.Run(protocol, func(t *testing.T) {
			m := manager(t)
			o := Options{Protocol: protocol, Interactive: true, User: "test", Password: "test"}
			if protocol == "ssh" {
				o.Address, o.HostKey = testutil.SSH(t)
			} else {
				o.Address = testutil.TelnetSwitch(t)
			}
			v, e := m.Connect(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			id := v.Session
			// Connect may include the greeting; wait for it only if not already returned.
			if !strings.Contains(v.Output, "switch> ") {
				v = run(t, m, "read", Request{Session: id, WaitFor: `switch> `, Wait: "1s"})
				if !strings.Contains(v.Output, "switch> ") {
					t.Fatalf("greeting: %+v", v)
				}
			}
			for _, step := range []struct{ data, pattern string }{{"enable\r\n", `switch# `}, {"configure terminal\r\n", `switch\(config\)# `}, {"interface ethernet 1\r\n", `switch\(config-if\)# `}, {"end\r\n", `switch# `}, {"show running-config\r\n", `--More--`}, {" ", `switch# `}, {"slow\r\n", `switch# `}} {
				v = run(t, m, "interact", Request{Session: id, Data: step.data, WaitFor: step.pattern, Timeout: "500ms", Wait: "2s"})
				if v.Reason != "waitfor" {
					t.Fatalf("%q: %+v", step.data, v)
				}
			}
			v = run(t, m, "interact", Request{Session: id, Data: strings.Repeat("x", InputLimit+1)})
			if !v.Backpressure || v.Reason != "backpressure" {
				t.Fatalf("unbounded input accepted: %+v", v)
			}
			v = run(t, m, "interact", Request{Session: id, Data: "quit\r\n", Wait: "1s"})
			if v.Connected || v.Error == "" || !strings.Contains(v.Hint, "connect") {
				t.Fatalf("disconnect: %+v", v)
			}
		})
	}
}
func TestWaitConditionsAndBusy(t *testing.T) {
	addr := testutil.Listen(t, func(c net.Conn) {
		_, _ = c.Write([]byte("hello"))
		b := make([]byte, 32)
		for {
			n, e := c.Read(b)
			if e != nil {
				return
			}
			if string(b[:n]) == "tick" {
				for i := 0; i < 10; i++ {
					_, _ = c.Write([]byte("x"))
					time.Sleep(20 * time.Millisecond)
				}
			}
		}
	})
	m := manager(t)
	v, e := m.Connect(context.Background(), Options{Protocol: "telnet", Address: addr})
	if e != nil {
		t.Fatal(e)
	}
	id := v.Session
	v = run(t, m, "read", Request{Session: id, Wait: "60ms", WaitFor: "NEVER", Timeout: "1s"})
	if v.Reason != "wait" {
		t.Fatalf("fixed: %+v", v)
	}
	start := time.Now()
	v = run(t, m, "interact", Request{Session: id, Data: "tick", Wait: "1s", Timeout: "50ms"})
	if v.Reason != "timeout" || time.Since(start) < 200*time.Millisecond || time.Since(start) > 800*time.Millisecond {
		t.Fatalf("idle reset: %+v", v)
	}
	done := make(chan Result, 1)
	go func() { r, _ := m.Run(context.Background(), "read", Request{Session: id, Wait: "1s"}); done <- r }()
	s, _ := m.Get(id)
	deadline := time.Now().Add(time.Second)
	for {
		if !s.op.TryLock() {
			break
		}
		s.op.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("operation never started")
		}
		time.Sleep(time.Millisecond)
	}
	v = run(t, m, "read", Request{Session: id})
	if v.Reason != "busy" || !v.Backpressure {
		t.Fatalf("busy: %+v", v)
	}
	_, _ = m.Close(id)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close waited for operation lock")
	}
}
func TestBoundedHistoryAndTail(t *testing.T) {
	s := &Session{id: "test", connected: true, notify: make(chan struct{}, 1)}
	_, _ = s.Write([]byte(strings.Repeat("x", HistoryLimit+100)))
	v := s.snapshot(Request{}, "test")
	if !v.Truncated || !v.More || len(v.Output) != DefaultOutputLimit || v.RetainedFrom != 100 {
		t.Fatalf("history bounds: %+v", v.statusSummary())
	}
	off := v.End
	next := s.snapshot(Request{Offset: &off}, "test")
	if next.Start != off {
		t.Fatal("paging did not advance")
	}
	_, _ = s.Write([]byte("\none\ntwo\nthree\n"))
	v = s.snapshot(Request{Tail: 2}, "test")
	if v.Output != "two\nthree\n" {
		t.Fatalf("tail %q", v.Output)
	}
}
func (r Result) statusSummary() string { return r.Reason }
func TestValidation(t *testing.T) {
	for _, r := range []Request{{Wait: "0s"}, {Wait: "-1s"}, {Wait: "forever"}, {Timeout: "0s"}, {WaitFor: "["}, {Tail: -1}} {
		if _, e := parseWait(r); e == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	if d, e := Duration("1min"); e != nil || d != time.Minute {
		t.Fatal(d, e)
	}
}

func TestInputWriteDeadlineAndBackpressure(t *testing.T) {
	t.Run("blocked write bounded by fixed wait", func(t *testing.T) {
		m := manager(t)
		a, b := net.Pipe()
		defer b.Close()
		s := &Session{id: "blocked", interactive: true, connected: true, last: time.Now(), notify: make(chan struct{}, 1), done: make(chan struct{}), input: a, closer: a}
		m.sessions[s.id] = s
		start := time.Now()
		v := run(t, m, "interact", Request{Session: s.id, Data: "x", InputID: "blocked-1", Wait: "30ms"})
		if v.Connected || v.Reason != "io_error" || time.Since(start) > time.Second {
			t.Fatalf("blocked write escaped deadline: %+v", v)
		}
		if v.InputState != "unknown" {
			t.Fatal("partial write claimed certainty", v)
		}
		v = run(t, m, "interact", Request{Session: s.id, Data: "x", InputID: "blocked-1"})
		if v.InputState != "duplicate" || v.InputSeq != 1 {
			t.Fatal("uncertain input replayed", v)
		}
	})
	t.Run("silent endpoint allows next completed write", func(t *testing.T) {
		addr := testutil.Listen(t, func(c net.Conn) {
			b := make([]byte, 32)
			for {
				if _, e := c.Read(b); e != nil {
					return
				}
			}
		})
		m := manager(t)
		v, e := m.Connect(context.Background(), Options{Protocol: "telnet", Address: addr})
		if e != nil {
			t.Fatal(e)
		}
		id := v.Session
		v = run(t, m, "interact", Request{Session: id, Data: "first", Wait: "20ms"})
		if v.Backpressure || !v.Connected || v.InputState != "sent" {
			t.Fatal(v)
		}
		v = run(t, m, "interact", Request{Session: id, Data: "second", Wait: "20ms"})
		if v.Backpressure || v.InputState != "sent" || v.InputSeq != 2 {
			t.Fatalf("completed write prevented further input: %+v", v)
		}
		v = run(t, m, "interact", Request{Session: id, Data: "\x03", Wait: "20ms"})
		if v.Reason == "backpressure" {
			t.Fatal("Ctrl-C rejected")
		}
	})
}

func TestSSHMissingExitStatusKeepsTransport(t *testing.T) {
	addr, key := testutil.SSH(t)
	m := manager(t)
	v, e := m.Connect(context.Background(), Options{Protocol: "ssh", Address: addr, User: "test", Password: "test", HostKey: key})
	if e != nil {
		t.Fatal(e)
	}
	id := v.Session
	v = run(t, m, "exec", Request{Session: id, Command: "missing-status", Wait: "1s"})
	if v.Reason != "exit_status_missing" || !v.Connected || v.Running || v.ExitCode != nil || v.Warning == "" || v.Error != "" || !strings.Contains(v.Output, "device output") {
		t.Fatalf("missing status misclassified: %+v", v)
	}
	v = run(t, m, "exec", Request{Session: id, Command: "both"})
	if !v.Connected || v.ExitCode == nil || *v.ExitCode != 0 || v.Warning != "" {
		t.Fatalf("transport lost or stale warning: %+v", v)
	}
}

// This deliberately models a terminal that treats CR and LF as separate Enters
// and replays the last command on an empty Enter. It is not a Huawei emulator.
func TestSSHWireOnceAndSeparateEnterReplay(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(map[bool]string{false: "line_CR", true: "raw_CRLF"}[raw], func(t *testing.T) {
			received := make(chan string, 1)
			addr, key := testutil.SSHWithShell(t, func(rw io.ReadWriter) {
				want := 9
				if raw {
					want = 10
				}
				b := make([]byte, want)
				if _, e := io.ReadFull(rw, b); e != nil {
					return
				}
				received <- string(b)
				last := ""
				line := ""
				for _, c := range b {
					if c == '\r' || c == '\n' {
						cmd := line
						line = ""
						if cmd == "" {
							cmd = last
						}
						last = cmd
						if cmd == "diagnose" {
							_, _ = io.WriteString(rw, "Warning: Enter diagnose view\r\n[~UNIONS-5-diagnose]\r\n")
						}
					} else {
						line += string(c)
					}
				}
				_, _ = io.Copy(io.Discard, rw)
			})
			m := manager(t)
			v, e := m.Connect(context.Background(), Options{Protocol: "ssh", Address: addr, User: "x", Password: "x", HostKey: key, Interactive: true})
			if e != nil {
				t.Fatal(e)
			}
			line := "diagnose"
			r := Request{Session: v.Session, Line: &line, Wait: "100ms", InputID: "enter-diagnose"}
			if raw {
				r.Line = nil
				r.Data = "diagnose\r\n"
			}
			v = run(t, m, "interact", r)
			wire := <-received
			want := "diagnose\r"
			count := 1
			if raw {
				want += "\n"
				count = 2
			}
			if wire != want || strings.Count(v.Output, "Warning:") != count || v.InputState != "sent" || v.InputSeq != 1 || v.InputBytes != len(want) {
				t.Fatalf("wire=%q result=%+v", wire, v)
			}
			dup := run(t, m, "interact", r)
			if dup.InputState != "duplicate" || dup.InputSeq != 1 {
				t.Fatalf("retransmitted logical input: %+v", dup)
			}
		})
	}
}
func TestDelayedOutputReadAndDeduplication(t *testing.T) {
	received := make(chan string, 4)
	addr := testutil.Listen(t, func(c net.Conn) {
		b := make([]byte, 128)
		for {
			n, e := c.Read(b)
			if e != nil {
				return
			}
			received <- string(b[:n])
			time.Sleep(100 * time.Millisecond)
			_, _ = c.Write([]byte("done\r\n[~UNIONS-5]"))
		}
	})
	m := manager(t)
	v, e := m.Connect(context.Background(), Options{Protocol: "telnet", Address: addr})
	if e != nil {
		t.Fatal(e)
	}
	id := v.Session
	line := "display device"
	r := Request{Session: id, Line: &line, Enter: "cr", InputID: "display-device-1", Wait: "20ms"}
	v = run(t, m, "interact", r)
	if v.Output != "" || v.InputState != "sent" || v.NextAction != "" {
		t.Fatalf("empty output looks unsent: %+v", v)
	}
	first := <-received
	if first != "display device\r" {
		t.Fatalf("wire %q", first)
	}
	v = run(t, m, "interact", r)
	if v.InputState != "duplicate" {
		t.Fatal(v)
	}
	other := "display version"
	r.Line = &other
	if _, e = m.Run(context.Background(), "interact", r); e == nil {
		t.Fatal("changed payload accepted under same ID")
	}
	v = run(t, m, "read", Request{Session: id, WaitFor: `\[~?UNIONS-5\]`, Wait: "1s"})
	if v.Reason != "waitfor" || !strings.Contains(v.Output, "done") || v.InputState != "none" || v.InputSeq != 1 {
		t.Fatalf("delayed output lost: %+v", v)
	}
	select {
	case x := <-received:
		t.Fatalf("unexpected replay %q", x)
	default:
	}
}
func TestLineValidation(t *testing.T) {
	m := manager(t)
	s := &Session{id: "line", interactive: true, done: make(chan struct{}), notify: make(chan struct{}, 1)}
	m.sessions[s.id] = s
	line := "a\nb"
	for _, r := range []Request{{Session: s.id, Line: &line}, {Session: s.id, Data: "x", Line: &line}, {Session: s.id, Enter: "cr"}, {Session: s.id, InputID: "without-input"}} {
		if _, e := m.Run(context.Background(), "interact", r); e == nil {
			t.Fatal("accepted malformed input", r)
		}
	}
}

func TestWriteDrainAllowsNextInput(t *testing.T) {
	m := manager(t)
	a, b := net.Pipe()
	defer b.Close()
	s := &Session{id: "drain", interactive: true, connected: true, last: time.Now(), notify: make(chan struct{}, 1), done: make(chan struct{}), input: a, closer: a}
	m.sessions[s.id] = s
	done := make(chan Result, 1)
	go func() {
		v, _ := m.Run(context.Background(), "interact", Request{Session: s.id, Data: "first", Wait: "200ms"})
		done <- v
	}()
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		seq := s.inputSeq
		s.mu.Unlock()
		if seq == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("write never started")
		}
		time.Sleep(time.Millisecond)
	}
	v := run(t, m, "interact", Request{Session: s.id, Data: "again", InputID: "retry-after-busy"})
	if v.Reason != "busy" || v.InputState != "not_sent" || !v.RetrySameInput || v.NextAction != "wait_current_call" {
		t.Fatal(v)
	}
	buf := make([]byte, 5)
	if _, e := io.ReadFull(b, buf); e != nil || string(buf) != "first" {
		t.Fatal(string(buf), e)
	}
	if v = <-done; v.InputState != "sent" || v.Backpressure {
		t.Fatal(v)
	}
	go func() { _, _ = io.ReadFull(b, buf) }()
	v = run(t, m, "interact", Request{Session: s.id, Data: "again", InputID: "retry-after-busy", Wait: "20ms"})
	if v.InputState != "sent" || v.InputSeq != 2 || v.Backpressure {
		t.Fatal(v)
	}
}

func TestShortSessionIDsAndConfiguredInputLimit(t *testing.T) {
	addr := testutil.Listen(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	m := manager(t)
	o := Options{Protocol: "telnet", Address: addr, MaxInputBytes: 8192}
	v, e := m.Connect(context.Background(), o)
	if e != nil || v.Session != "s1" {
		t.Fatal(v, e)
	}
	id := v.Session
	v = run(t, m, "interact", Request{Session: id, Data: strings.Repeat("x", 8192), Wait: "20ms"})
	if v.InputState != "sent" {
		t.Fatal(v)
	}
	v = run(t, m, "interact", Request{Session: id, Data: strings.Repeat("x", 8193), Wait: "20ms"})
	if v.InputState != "not_sent" || !v.Backpressure || v.InputSeq != 1 {
		t.Fatal(v)
	}
	_, _ = m.Close(id)
	v, e = m.Connect(context.Background(), o)
	if e != nil || v.Session != "s2" {
		t.Fatal(v, e)
	}
	for _, n := range []int{-1, MaxInputLimit + 1} {
		o.MaxInputBytes = n
		if _, e = m.Connect(context.Background(), o); e == nil {
			t.Fatal("invalid limit accepted", n)
		}
	}
}

// Use portable socket buffer controls rather than OS-specific TCP queue queries.
func TestTCPFullSendBufferDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.TCPConn, 1)
	go func() {
		c, e := listener.Accept()
		if e == nil {
			accepted <- c.(*net.TCPConn)
		}
	}()
	c, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	peer := <-accepted
	defer peer.Close()
	if err = peer.SetReadBuffer(1024); err != nil {
		t.Fatal(err)
	}
	if err = c.(*net.TCPConn).SetWriteBuffer(1024); err != nil {
		t.Fatal(err)
	}
	m := manager(t)
	s := &Session{id: "tcpfull", maxInputBytes: MaxInputLimit, interactive: true, connected: true, last: time.Now(), notify: make(chan struct{}, 1), done: make(chan struct{}), input: c, closer: c}
	m.sessions[s.id] = s
	start := time.Now()
	v := run(t, m, "interact", Request{Session: s.id, Data: strings.Repeat("x", MaxInputLimit), Wait: "100ms", InputID: "full"})
	if v.Reason != "io_error" || v.Connected || v.InputState != "unknown" || time.Since(start) > time.Second {
		t.Fatalf("full TCP buffer escaped deadline: %+v", v)
	}
	v = run(t, m, "interact", Request{Session: s.id, Data: strings.Repeat("x", MaxInputLimit), InputID: "full"})
	if v.InputState != "duplicate" {
		t.Fatal("uncertain write replayed", v)
	}
}

func TestCloseDoesNotReadOutput(t *testing.T) {
	m := manager(t)
	s := &Session{id: "close", connected: true, notify: make(chan struct{}, 1), done: make(chan struct{})}
	m.sessions[s.id] = s
	_, _ = s.Write([]byte(strings.Repeat("unread console replay", 1000)))
	v, err := m.Close(s.id)
	if err != nil || v.Output != "" || v.Error != "" || v.Hint != "" || v.NextAction != "" || v.Connected || v.Reason != "closed" || s.cursor != 0 {
		t.Fatal(v, err)
	}
	if _, err = m.Get(s.id); err == nil {
		t.Fatal("closed session retained")
	}
}

func TestOutputBudgetPreservesUnreadData(t *testing.T) {
	s := &Session{id: "output", connected: true, notify: make(chan struct{}, 1)}
	text := strings.Repeat("0123456789", 2000)
	_, _ = s.Write([]byte(text))
	var got strings.Builder
	for {
		v := s.snapshot(Request{MaxOutputBytes: 317}, "wait")
		if len(v.Output) > 317 || v.Truncated || v.End-v.Start != int64(len(v.Output)) {
			t.Fatal(v)
		}
		got.WriteString(v.Output)
		if !v.More {
			break
		}
	}
	if got.String() != text {
		t.Fatal("budget lost or repeated output")
	}
	off := int64(10)
	v := s.snapshot(Request{Offset: &off, MaxOutputBytes: 5}, "wait")
	if v.Output != text[10:15] || s.cursor != int64(len(text)) {
		t.Fatal(v)
	}
	for _, n := range []int{-1, ResultLimit + 1} {
		if _, err := parseWait(Request{MaxOutputBytes: n}); err == nil {
			t.Fatal("invalid output limit", n)
		}
	}
}

func TestSessionExpirationSeparatesUnusedAndActive(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name                         string
		interactive, running, active bool
		lastAge, callAge, useAge     time.Duration
		idle                         time.Duration
		disconnected                 bool
		reason                       string
	}{
		{name: "console between calls", interactive: true, running: true, lastAge: 10 * time.Minute, useAge: 5 * time.Minute, idle: SessionIdleLimit},
		{name: "unused lifetime expired", interactive: true, lastAge: time.Hour, useAge: time.Hour, idle: SessionIdleLimit, disconnected: true, reason: "session_idle"},
		{name: "unused lifetime disabled", interactive: true, lastAge: time.Hour, useAge: time.Hour},
		{name: "new call after long silence", interactive: true, active: true, lastAge: time.Hour, callAge: time.Second, idle: SessionIdleLimit},
		{name: "active console silence", interactive: true, active: true, lastAge: time.Hour, callAge: IdleLimit, disconnected: true, reason: "endpoint_idle"},
		{name: "exec between polls", running: true, lastAge: IdleLimit, disconnected: true, reason: "endpoint_idle"},
		{name: "poll does not reset exec silence", running: true, active: true, lastAge: IdleLimit, callAge: time.Second, disconnected: true, reason: "endpoint_idle"},
		{name: "active output resets silence", interactive: true, active: true, lastAge: time.Second, callAge: IdleLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{interactive: tc.interactive, running: tc.running, connected: true, last: now.Add(-tc.lastAge), lastUse: now.Add(-tc.useAge), idleTimeout: tc.idle, notify: make(chan struct{}, 1), done: make(chan struct{})}
			if tc.active {
				s.activeSince = now.Add(-tc.callAge)
			}
			s.expire(now)
			if s.connected == tc.disconnected || !strings.Contains(s.failure, tc.reason) {
				t.Fatalf("connected=%v failure=%s", s.connected, s.failure)
			}
		})
	}
}

func TestIdleTimeoutConfiguration(t *testing.T) {
	addr := testutil.Listen(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	m := manager(t)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"", SessionIdleLimit}, {"0", 0}, {"10min", 10 * time.Minute}} {
		v, err := m.Connect(context.Background(), Options{Protocol: "telnet", Address: addr, IdleTimeout: tc.value})
		if err != nil {
			t.Fatal(err)
		}
		s, _ := m.Get(v.Session)
		if s.idleTimeout != tc.want {
			t.Fatal(s.idleTimeout, tc.want)
		}
	}
	for _, value := range []string{"-1s", "nonsense", "0s"} {
		if _, err := m.Connect(context.Background(), Options{Protocol: "telnet", Address: addr, IdleTimeout: value}); err == nil {
			t.Fatal("invalid idle lifetime", value)
		}
	}
}

func TestEscapedInputDecodesOnceAndDeduplicatesBytes(t *testing.T) {
	m := manager(t)
	var wire bytes.Buffer
	s := &Session{id: "escaped", interactive: true, connected: true, last: time.Now(), notify: make(chan struct{}, 1), done: make(chan struct{}), input: &wire, maxInputBytes: 4}
	m.sessions[s.id] = s
	escaped := `\x03\r`
	v := run(t, m, "interact", Request{Session: s.id, DataEscaped: &escaped, InputID: "same", Wait: "1ms"})
	if v.InputState != "sent" || v.InputBytes != 2 || wire.String() != "\x03\r" {
		t.Fatal(v, wire.Bytes())
	}
	v = run(t, m, "interact", Request{Session: s.id, Data: "\x03\r", InputID: "same"})
	if v.InputState != "duplicate" || wire.Len() != 2 {
		t.Fatal(v, wire.Bytes())
	}
	escaped = `\x41\x42\x43\x44\x45`
	v = run(t, m, "interact", Request{Session: s.id, DataEscaped: &escaped, Wait: "1ms"})
	if v.InputState != "not_sent" || wire.Len() != 2 {
		t.Fatal("decoded size limit not enforced", v)
	}
	escaped = `\q`
	if _, err := m.Run(context.Background(), "interact", Request{Session: s.id, DataEscaped: &escaped}); err == nil || wire.Len() != 2 {
		t.Fatal("invalid escape sent input", err)
	}
	escaped = `\r`
	for _, r := range []Request{{Session: s.id, DataEscaped: &escaped, Data: "x"}, {Session: s.id, DataEscaped: &escaped, Key: "enter"}, {Session: s.id, DataEscaped: &escaped, Enter: "cr"}} {
		if _, err := m.Run(context.Background(), "interact", r); err == nil {
			t.Fatal("ambiguous escaped input accepted", r)
		}
	}
}

func TestDecodeVisibleEscapes(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`\r\n\t\b\f\v\a`, "\r\n\t\b\f\v\a"},
		{`\x00\xff`, string([]byte{0, 255})},
		{`\u4e2d\U0001f600`, "中😀"},
		{`\\r`, `\r`},
		{`echo "a" 'b'`, `echo "a" 'b'`},
		{`&#13;`, `&#13;`},
		{`\003`, "\x03"},
	} {
		got, err := decodeInput(tc.source)
		if err != nil || got != tc.want {
			t.Fatalf("%q got %q want %q err=%v", tc.source, got, tc.want, err)
		}
	}
	for _, source := range []string{`\`, `\x0`, `\xGG`, `\uD800`, `\U00110000`, `\q`} {
		if _, err := decodeInput(source); err == nil {
			t.Fatal("invalid escape accepted", source)
		}
	}
}
