package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
)

func TestReconnectTelnetKeepsHandleConfigAndNeverReplays(t *testing.T) {
	var serial atomic.Int32
	inputs := make(chan string, 4)
	addr := testutil.Listen(t, func(c net.Conn) {
		n := serial.Add(1)
		_, _ = fmt.Fprintf(c, "connection-%d> ", n)
		b, _ := io.ReadAll(c)
		inputs <- string(b)
	})
	m := manager(t)
	id := connect(t, m, options(addr, "telnet", "stream"))
	run(t, m, "read_until", Request{Session: id, Config: cfg(map[string]any{"end_pattern": "connection-1> "})})
	run(t, m, "send_text", Request{Session: id, Data: "old-command", CollectOutput: noCollect()})
	if _, err := m.Configure(id, cfg(map[string]any{"read_only": true, "line_end": `\0`, "max_output_bytes": 1024})); err != nil {
		t.Fatal(err)
	}
	old, _ := m.Get(id)
	v, err := m.Reconnect(context.Background(), id)
	if err != nil || !v.Connected || v.Session != id || v.Output != "" {
		t.Fatal(v, err)
	}
	select {
	case b := <-inputs:
		if b != "old-command" {
			t.Fatal(b)
		}
	case <-time.After(time.Second):
		t.Fatal("old transport not closed")
	}
	next, _ := m.Get(id)
	if next == old {
		t.Fatal("reused old transport instance")
	}
	info, _ := m.GetConfig(id)
	if !info.Config.ReadOnly || info.Config.LineEnd != `\0` || info.Config.MaxOutputBytes != 1024 {
		t.Fatal(info)
	}
	v = run(t, m, "read_until", Request{Session: id, Config: cfg(map[string]any{"end_pattern": "connection-2> "})})
	if strings.Contains(v.Output, "connection-1") || !strings.Contains(v.Output, "connection-2") || v.Start != 0 {
		t.Fatal(v)
	}
	v = run(t, m, "read_history", Request{Session: id})
	if strings.Contains(v.Output, "connection-1") {
		t.Fatal("history replayed", v)
	}
	if v = run(t, m, "send_text", Request{Session: id, Data: "blocked"}); v.InputState != "not_sent" {
		t.Fatal(v)
	}
	if _, err = m.Close(id); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-inputs:
		if b != "" {
			t.Fatal("reconnect sent application input", b)
		}
	case <-time.After(time.Second):
		t.Fatal("new transport not closed")
	}
	if _, err = m.Reconnect(context.Background(), id); err == nil {
		t.Fatal("closed handle reconnected")
	}
}

func TestReconnectSSHExecAndStream(t *testing.T) {
	addr, key := testutil.SSH(t)
	for _, mode := range []string{"exec", "stream"} {
		t.Run(mode, func(t *testing.T) {
			m := manager(t)
			o := options(addr, "ssh", mode)
			o.HostKey = key
			id := connect(t, m, o)
			if mode == "exec" {
				run(t, m, "exec", Request{Session: id, Command: "both"})
			}
			v, err := m.Reconnect(context.Background(), id)
			if err != nil || !v.Connected || v.Session != id || v.Running || v.ExitCode != nil {
				t.Fatal(v, err)
			}
			if mode == "exec" {
				v = run(t, m, "exec", Request{Session: id, Command: "exit7"})
				if v.ExitCode == nil || *v.ExitCode != 7 {
					t.Fatal(v)
				}
			} else {
				v = run(t, m, "read_until", Request{Session: id, Config: cfg(map[string]any{"end_pattern": "switch> "})})
				if !strings.Contains(v.Output, "switch> ") {
					t.Fatal(v)
				}
			}
		})
	}
}

func TestReconnectBusyDoesNotDisconnect(t *testing.T) {
	m, s, _ := localSession(t)
	for _, kind := range []string{"read", "write", "config", "active", "running"} {
		t.Run(kind, func(t *testing.T) {
			switch kind {
			case "read":
				s.readMu.Lock()
				defer s.readMu.Unlock()
			case "write":
				s.writeMu.Lock()
				defer s.writeMu.Unlock()
			case "config":
				s.configMu.Lock()
				defer s.configMu.Unlock()
			case "active":
				s.active++
				defer func() { s.active-- }()
			case "running":
				s.running = true
				defer func() { s.running = false }()
			}
			if _, err := m.Reconnect(context.Background(), s.id); err == nil || !strings.Contains(err.Error(), "busy") {
				t.Fatal(err)
			}
			if !s.status().Connected {
				t.Fatal("busy reconnect closed connection")
			}
		})
	}
}

// A controllable SSH proxy allows failure/cancellation on the original address.
func reconnectProxy(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	backend, _ := testutil.SSH(t)
	mode := new(atomic.Int32)
	addr := testutil.Listen(t, func(c net.Conn) {
		switch mode.Load() {
		case 1:
			return
		case 2:
			_, _ = io.Copy(io.Discard, c)
			return
		}
		upstream, err := net.Dial("tcp", backend)
		if err != nil {
			return
		}
		defer upstream.Close()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, c); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(c, upstream)
		_ = c.Close()
		<-done
	})
	return addr, mode
}
func TestReconnectFailureRetainsParametersAndCancellation(t *testing.T) {
	addr, mode := reconnectProxy(t)
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	mode.Store(1)
	v, err := m.Reconnect(context.Background(), id)
	if err != nil || v.Connected || v.Error == "" || v.NextAction != "reconnect" {
		t.Fatal(v, err)
	}
	mode.Store(0)
	v, err = m.Reconnect(context.Background(), id)
	if err != nil || !v.Connected || v.Session != id {
		t.Fatal(v, err)
	}
	mode.Store(2)
	if _, err = m.Configure(id, cfg(map[string]any{"connect_timeout_ms": 30})); err != nil {
		t.Fatal(err)
	}
	startTimeout := time.Now()
	v, err = m.Reconnect(context.Background(), id)
	if err != nil || v.Connected || v.Error == "" || time.Since(startTimeout) > time.Second {
		t.Fatal("current connect timeout not used", v, err)
	}
	if _, err = m.Configure(id, cfg(map[string]any{"connect_timeout_ms": 10000})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	v, err = m.Reconnect(ctx, id)
	if err != nil || v.Connected || v.Error == "" || time.Since(start) > time.Second {
		t.Fatal(v, err)
	}
	mode.Store(0)
	v, err = m.Reconnect(context.Background(), id)
	if err != nil || !v.Connected {
		t.Fatal(v, err)
	}
}
func TestCloseCancelsReconnectWithoutResurrection(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprint(all), func(t *testing.T) {
			addr, mode := reconnectProxy(t)
			m := manager(t)
			id := connect(t, m, options(addr, "ssh", "exec"))
			mode.Store(2)
			done := make(chan error, 1)
			go func() { _, err := m.Reconnect(context.Background(), id); done <- err }()
			deadline := time.Now().Add(time.Second)
			for {
				m.mu.Lock()
				pending := m.pending[id] != nil
				m.mu.Unlock()
				if pending {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("reconnect not pending")
				}
				time.Sleep(time.Millisecond)
			}
			if all {
				m.CloseAll()
			} else {
				if _, err := m.Close(id); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("close did not invalidate reconnect")
				}
			case <-time.After(time.Second):
				t.Fatal("reconnect not cancelled")
			}
			if len(m.List()) != 0 {
				t.Fatal("closed session resurrected")
			}
		})
	}
}
