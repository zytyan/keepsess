package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
)

func TestTelnetSSHJumpsDeviceAndReconnect(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(strings.Repeat("hop-", count), func(t *testing.T) {
			target := testutil.TelnetSwitch(t)
			o := options(target, "telnet", "")
			next := target
			var stats []*testutil.JumpStats
			for i := 0; i < count; i++ {
				addr, key, state := testutil.SSHJump(t, next, false)
				o.SSHJumps = append([]SSHJump{jumpOptions(addr, key)}, o.SSHJumps...)
				next = addr
				stats = append(stats, state)
			}
			o.Config = cfg(map[string]any{"read_idle_timeout_ms": 20})
			m := manager(t)
			id := connect(t, m, o)
			read := func(pattern string) Result {
				return run(t, m, "read_until", Request{Session: id, Config: cfg(map[string]any{"read_idle_timeout_ms": 0, "read_total_timeout_ms": 2000, "end_pattern": pattern})})
			}
			if v := read("switch> "); !strings.Contains(v.Output, "switch> ") {
				t.Fatal(v)
			}
			for _, tc := range []struct{ command, pattern string }{
				{"enable", "switch# "}, {"configure terminal", `switch\(config\)# `},
				{"end", "switch# "}, {"show running-config", "--More--"},
			} {
				run(t, m, "sendline_text", Request{Session: id, Data: tc.command, CollectOutput: noCollect()})
				if v := read(tc.pattern); v.Reason != "match" {
					t.Fatal(v)
				}
			}
			run(t, m, "send_text", Request{Session: id, Data: " ", CollectOutput: noCollect()})
			if v := read("switch# "); !strings.Contains(v.Output, "interface ethernet 1") {
				t.Fatal(v)
			}
			// Normal read timeouts preserve the chain, and exec never sends input.
			v := run(t, m, "read_until", Request{Session: id, Config: cfg(map[string]any{"read_total_timeout_ms": 20, "read_idle_timeout_ms": 0})})
			if !v.Connected || v.Reason != "total_timeout" {
				t.Fatal(v)
			}
			if _, err := m.Operate(context.Background(), "exec", Request{Session: id, Command: "must-not-run"}); err == nil {
				t.Fatal("Telnet accepted exec")
			}
			v, err := m.Reconnect(context.Background(), id)
			if err != nil || !v.Connected || v.Session != id || v.Peer != nil {
				t.Fatal(v, err)
			}
			if v = read("switch> "); strings.Contains(v.Output, "switch# ") || !strings.Contains(v.Output, "switch> ") {
				t.Fatal(v)
			}
			m.CloseAll()
			for _, state := range stats {
				eventually(t, func() bool { return state.Active.Load() == 0 })
			}
		})
	}
}

func TestTelnetSSHJumpIACReadOnlyAndExactInput(t *testing.T) {
	receipts := make(chan []byte, 2)
	target := testutil.Listen(t, func(c net.Conn) {
		// Fragment IAC and send a literal escaped IAC in application output.
		for _, b := range []byte{255, 251, 1, 'A', 255, 255, 'B', '>'} {
			_, _ = c.Write([]byte{b})
		}
		negotiation := make([]byte, 3)
		_, _ = io.ReadFull(c, negotiation)
		receipts <- negotiation
		input := make([]byte, 5)
		_, _ = io.ReadFull(c, input)
		receipts <- input
		_, _ = io.WriteString(c, "DONE>")
		_, _ = io.Copy(io.Discard, c)
	})
	addr, key, _ := testutil.SSHJump(t, target, false)
	o := options(target, "telnet", "stream")
	o.SSHJumps = []SSHJump{jumpOptions(addr, key)}
	o.Config = cfg(map[string]any{"read_only": true})
	m := manager(t)
	id := connect(t, m, o)
	v := run(t, m, "read_until", Request{Session: id, Config: cfg(map[string]any{"end_pattern": ">", "read_idle_timeout_ms": 0, "read_total_timeout_ms": 2000})})
	if !strings.Contains(v.Output, `A\xFFB>`) {
		t.Fatal(v)
	}
	if v = run(t, m, "send_text", Request{Session: id, Data: "blocked"}); v.InputState != "not_sent" {
		t.Fatal(v)
	}
	select {
	case got := <-receipts:
		if !bytes.Equal(got, []byte{255, 253, 1}) {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("Telnet negotiation failed through SSH")
	}
	if _, err := m.Configure(id, cfg(map[string]any{"read_only": false})); err != nil {
		t.Fatal(err)
	}
	v = run(t, m, "send_escaped", Request{Session: id, Data: `u\xFF\r\n`, Config: cfg(map[string]any{"end_pattern": "DONE>", "read_idle_timeout_ms": 0, "read_total_timeout_ms": 2000})})
	if v.InputState != "sent" || !strings.Contains(v.Output, "DONE>") {
		t.Fatal(v)
	}
	select {
	case got := <-receipts:
		if !bytes.Equal(got, []byte{'u', 255, 255, '\r', '\n'}) {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("input never reached Telnet target")
	}
}

func TestTelnetSSHJumpKeepalive(t *testing.T) {
	target := testutil.TelnetSwitch(t)
	addr, key, state := testutil.SSHJump(t, target, false)
	m := manager(t)
	o := options(target, "telnet", "stream")
	o.SSHJumps = []SSHJump{jumpOptions(addr, key)}
	o.Config = cfg(map[string]any{"ssh_keepalive_ms": 10, "ssh_keepalive_timeout_ms": 200})
	id := connect(t, m, o)
	s, _ := m.Get(id)
	eventually(t, func() bool { return state.Probes.Load() >= 2 })
	if !s.status().Connected {
		t.Fatal("negative SSH keepalive reply disconnected Telnet target")
	}
	state.IgnoreKeepalive.Store(true)
	eventually(t, func() bool { return !s.status().Connected })
	if v := s.status(); v.DisconnectReason != "keepalive_timeout" || v.NextAction != "reconnect" {
		t.Fatal(v)
	}
}

func TestSSHTelnetConnWriteDeadlineClosesTransport(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	s := &Session{connected: true, closer: a, done: make(chan struct{}), notify: make(chan struct{}, 1)}
	c := &sshTelnetConn{Conn: a, session: s}
	_ = c.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
	start := time.Now()
	_, err := c.Write([]byte("blocked"))
	if !errors.Is(err, os.ErrDeadlineExceeded) || time.Since(start) > time.Second || s.status().Connected {
		t.Fatal(err, s.status())
	}
}

func TestTelnetSSHJumpForwardTimeoutAndCancellation(t *testing.T) {
	target := testutil.TelnetSwitch(t)
	addr, key, state := testutil.SSHJump(t, target, true)
	for _, action := range []string{"timeout", "cancel", "close_all"} {
		t.Run(action, func(t *testing.T) {
			m := manager(t)
			o := options(target, "telnet", "stream")
			o.SSHJumps = []SSHJump{jumpOptions(addr, key)}
			o.Config = cfg(map[string]any{"connect_timeout_ms": 100})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			before := state.Forwards.Load()
			finished := make(chan error, 1)
			go func() { _, err := m.Connect(ctx, o); finished <- err }()
			if action != "timeout" {
				eventually(t, func() bool { return state.Forwards.Load() > before })
				if action == "cancel" {
					cancel()
				} else {
					m.CloseAll()
				}
			}
			select {
			case err := <-finished:
				if err == nil || !strings.Contains(err.Error(), "Telnet target") {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("forward to Telnet target failed to stop")
			}
			eventually(t, func() bool { return state.Active.Load() == 0 })
			if len(m.List()) != 0 {
				t.Fatal("failed Telnet connect retained a handle")
			}
		})
	}
}
