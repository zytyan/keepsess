package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func TestSSHKeepaliveNegativeReplyIsHealthyAndDoesNotRefreshIdle(t *testing.T) {
	var probes atomic.Int32
	addr, _ := testutil.SSHWithGlobalRequests(t, func(req *ssh.Request) {
		if req.Type == "keepalive@openssh.com" {
			probes.Add(1)
		}
		_ = req.Reply(false, nil)
	})
	m := manager(t)
	o := options(addr, "ssh", "exec")
	o.Config = cfg(map[string]any{"ssh_keepalive_ms": 10, "ssh_keepalive_timeout_ms": 100, "read_only": true})
	id := connect(t, m, o)
	s, _ := m.Get(id)
	s.mu.Lock()
	lastUse := s.lastUse
	s.mu.Unlock()
	eventually(t, func() bool { return probes.Load() >= 3 })
	s.mu.Lock()
	unchanged := s.lastUse.Equal(lastUse)
	s.mu.Unlock()
	if !s.status().Connected || !unchanged {
		t.Fatal("probe disconnected or refreshed tool idle")
	}
	if s.expire(lastUse.Add(time.Hour)) {
		t.Fatal("default session expired before 2h")
	}
	if _, err := m.Configure(id, cfg(map[string]any{"ssh_keepalive_ms": 0})); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	n := probes.Load()
	time.Sleep(40 * time.Millisecond)
	if probes.Load() != n {
		t.Fatal("disabled probes continue")
	}
	if !s.expire(time.Now().Add(2*time.Hour + time.Second)) {
		t.Fatal("idle session not reclaimed")
	}
	_, err := m.Get(id)
	var lookup *SessionLookupError
	if !errors.As(err, &lookup) || lookup.Reason != "session_idle_expired" || lookup.NextAction != "connect" {
		t.Fatal(err)
	}
}

func TestSSHKeepaliveTimeoutAndReconnect(t *testing.T) {
	var answer atomic.Bool
	addr, _ := testutil.SSHWithGlobalRequests(t, func(req *ssh.Request) {
		if answer.Load() {
			_ = req.Reply(true, nil)
		}
	})
	m := manager(t)
	o := options(addr, "ssh", "exec")
	o.Config = cfg(map[string]any{"ssh_keepalive_ms": 10, "ssh_keepalive_timeout_ms": 30})
	id := connect(t, m, o)
	a := run(t, m, "exec", Request{Session: id, Command: "hang", CollectOutput: noCollect()})
	s, _ := m.Get(id)
	eventually(t, func() bool { return !s.status().Connected })
	v := s.status()
	if v.DisconnectReason != "keepalive_timeout" || v.NextAction != "reconnect" {
		t.Fatal(v)
	}
	v = run(t, m, "task_status", Request{Handle: a.Handle})
	if v.State != "unknown" || v.DisconnectReason != "keepalive_timeout" {
		t.Fatal(v)
	}
	answer.Store(true)
	v, err := m.Reconnect(context.Background(), id)
	if err != nil || !v.Connected {
		t.Fatal(v, err)
	}
}

func TestDisconnectClassificationAndTombstoneBounds(t *testing.T) {
	for _, tc := range []struct {
		err          error
		detail, want string
	}{
		{&net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, "", "reset_by_peer"},
		{&net.DNSError{IsTimeout: true}, "", "transport_timeout"},
		{io.EOF, "", "remote_eof"},
		{errors.New("ssh: disconnect, reason 11: idle timeout"), "ssh: disconnect, reason 11: idle timeout", "ssh_disconnect"},
		{errors.New("broken pipe"), "broken pipe", "transport_error"},
	} {
		if got := classifyDisconnect(tc.err, tc.detail); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
	m, s, _ := localSession(t)
	s.disconnectError(&net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, "SSH disconnected")
	v := run(t, m, "read_available", Request{Session: s.id})
	if v.DisconnectReason != "reset_by_peer" || v.NextAction != "reconnect" || !strings.Contains(v.Error, "reset") {
		t.Fatal(v)
	}
	if _, err := m.Close(s.id); err != nil {
		t.Fatal(err)
	}
	_, err := m.Get(s.id)
	var lookup *SessionLookupError
	if !errors.As(err, &lookup) || lookup.Reason != "explicit_close" {
		t.Fatal(err)
	}
	m.mu.Lock()
	for i := 0; i < retiredLimit+10; i++ {
		m.rememberLocked(fmt.Sprint(i), "explicit_close")
	}
	if len(m.retired) != retiredLimit {
		t.Fatal(len(m.retired))
	}
	m.retired["old"] = retiredSession{"session_idle_expired", time.Now().Add(-3 * time.Hour)}
	err = m.sessionErrorLocked("old")
	if !strings.Contains(err.Error(), "unknown_handle") {
		t.Fatal(err)
	}
	m.mu.Unlock()
}

func TestSSHIdentityChangedOnlyAtConnectReconnect(t *testing.T) {
	a, keyA := testutil.SSH(t)
	b, keyB := testutil.SSH(t)
	var second atomic.Bool
	addr := testutil.Listen(t, func(c net.Conn) {
		target := a
		if second.Load() {
			target = b
		}
		up, err := net.Dial("tcp", target)
		if err != nil {
			return
		}
		defer up.Close()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(up, c); _ = up.Close(); close(done) }()
		_, _ = io.Copy(c, up)
		_ = c.Close()
		<-done
	})
	m := manager(t)
	v, err := m.Connect(context.Background(), options(addr, "ssh", "exec"))
	if err != nil || v.Peer == nil || v.Peer.HostKey != keyA || v.Peer.Server == "" || v.Peer.HostKeyChanged {
		t.Fatal(v, err)
	}
	id := v.Session
	if m.List()[0].Peer != nil {
		t.Fatal("identity repeated in list")
	}
	second.Store(true)
	v, err = m.Reconnect(context.Background(), id)
	if err != nil || v.Peer == nil || v.Peer.HostKey != keyB || !v.Peer.HostKeyChanged {
		t.Fatal(v, err)
	}
	v, err = m.Reconnect(context.Background(), id)
	if err != nil || v.Peer.HostKeyChanged {
		t.Fatal(v, err)
	}
}

func TestNewBudgetsAndRPCBudgetDoesNotTERM(t *testing.T) {
	c := DefaultConfig("ssh")
	if c.ExecTotalTimeoutMS != 120000 || c.SessionIdleTimeoutMS != 7200000 || c.MaxOutputBytes != 65536 {
		t.Fatal(c)
	}
	if _, err := applyConfig(c, cfg(map[string]any{"exec_total_timeout_ms": 300000, "max_output_bytes": 524288}), true); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ssh_keepalive_ms", "ssh_keepalive_timeout_ms"} {
		if _, err := applyConfig(c, cfg(map[string]any{key: 10}), true); err == nil {
			t.Fatal("override accepted", key)
		}
	}
	addr, signals := taskServer(t)
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	a := run(t, m, "exec", Request{Session: id, Command: "silent", CollectOutput: noCollect()})
	// Emulate the short remaining RPC budget without waiting 115 seconds.
	task, err := m.acquireTask(a.Handle)
	if err != nil {
		t.Fatal(err)
	}
	defer task.out.touch(-1)
	defer task.session.touch(-1)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	ctx = context.WithValue(ctx, callDeadlineKey{}, time.Now().Add(time.Second+80*time.Millisecond))
	v := task.wait(ctx, c)
	if v.Signal != nil || !v.Running || v.Handle == "" {
		t.Fatal(v)
	}
	select {
	case sig := <-signals:
		t.Fatal("RPC deadline signalled", sig)
	default:
	}
}

func TestOutputPagingNewDefaultAndMaximum(t *testing.T) {
	m, s, _ := localSession(t)
	_, _ = s.Write([]byte(strings.Repeat("x", 600<<10)))
	v := run(t, m, "read_available", Request{Session: s.id})
	if len(v.Output) != 64<<10 || !v.More || v.End != 64<<10 {
		t.Fatal("default page", len(v.Output), v.End, v.More)
	}
	v = run(t, m, "read_available", Request{Session: s.id, Config: cfg(map[string]any{"max_output_bytes": 512 << 10})})
	if len(v.Output) != 512<<10 || !v.More {
		t.Fatal("max page", len(v.Output), v.More)
	}
	v = run(t, m, "read_available", Request{Session: s.id})
	if len(v.Output) != 24<<10 || v.More {
		t.Fatal("last page", len(v.Output), v.More)
	}
}
