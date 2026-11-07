package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func jumpOptions(addr, key string) SSHJump {
	o := options(addr, "ssh", "exec")
	return SSHJump{Host: o.Host, Port: o.Port, Username: "jump-user", Password: "jump-secret", HostKey: key}
}

func TestSSHJumpChainExecStreamAndReconnect(t *testing.T) {
	for _, mode := range []string{"exec", "stream"} {
		for _, count := range []int{1, 2} {
			t.Run(mode+strings.Repeat("-hop", count), func(t *testing.T) {
				target, key := testutil.SSH(t)
				o := options(target, "ssh", mode)
				o.HostKey = key
				next := target
				var stats []*testutil.JumpStats
				for i := 0; i < count; i++ {
					addr, hostKey, state := testutil.SSHJump(t, next, false)
					o.Jumps = append([]SSHJump{jumpOptions(addr, hostKey)}, o.Jumps...)
					stats = append(stats, state)
					next = addr
				}
				m := manager(t)
				id := connect(t, m, o)
				// Mutating caller-owned hop storage cannot change reconnect credentials.
				o.Jumps[0].Password = "caller-mutated"
				check := func() {
					t.Helper()
					var v Result
					if mode == "exec" {
						v = run(t, m, "exec", Request{Session: id, Command: "both"})
						if v.ExitCode == nil || *v.ExitCode != 0 || !strings.Contains(v.Output, "stderr") {
							t.Fatal(v)
						}
					} else {
						v = run(t, m, "sendline_text", Request{Session: id, Data: "display version"})
						if v.InputState != "sent" || v.Output == "" {
							t.Fatal(v)
						}
					}
				}
				check()
				v, err := m.Reconnect(context.Background(), id)
				if err != nil || v.Session != id || !v.Connected || v.Peer == nil || v.Peer.HostKey != key {
					t.Fatal(v, err)
				}
				check()
				b, _ := json.Marshal(m.List())
				if strings.Contains(string(b), "jump-secret") || strings.Contains(string(b), "jump-user") || strings.Contains(string(b), "jumps") {
					t.Fatal(string(b))
				}
				if _, err := m.Close(id); err != nil {
					t.Fatal(err)
				}
				for _, state := range stats {
					eventually(t, func() bool { return state.Active.Load() == 0 })
					if state.Forwards.Load() != 2 || state.Sessions.Load() != 0 {
						t.Fatal("hop order/forwarding count or shell command on jump", state.Forwards.Load(), state.Sessions.Load())
					}
				}
			})
		}
	}
}

func TestSSHJumpVerificationAuthenticationAndFailureCleanup(t *testing.T) {
	target, targetKey := testutil.SSH(t)
	addr, key, state := testutil.SSHJump(t, target, false)
	for _, failure := range []string{"jump-key", "target-key", "password", "forward-denied"} {
		t.Run(failure, func(t *testing.T) {
			m := manager(t)
			o := options(target, "ssh", "exec")
			o.HostKey = targetKey
			o.Jumps = []SSHJump{jumpOptions(addr, key)}
			want := "SSH jump 1"
			switch failure {
			case "jump-key":
				o.Jumps[0].HostKey = "SHA256:bad"
			case "target-key":
				o.HostKey = "SHA256:bad"
				want = "SSH target"
			case "password":
				o.Jumps[0].Password = "bad-secret"
			case "forward-denied":
				o.Port++
			}
			_, err := m.Connect(context.Background(), o)
			if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "bad-secret") || len(m.List()) != 0 {
				t.Fatal(err)
			}
			eventually(t, func() bool { return state.Active.Load() == 0 })
		})
	}
}

func TestSSHJumpStalledForwardAndHandshake(t *testing.T) {
	for _, phase := range []string{"forward", "handshake"} {
		t.Run(phase, func(t *testing.T) {
			target := testutil.Listen(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
			addr, key, state := testutil.SSHJump(t, target, phase == "forward")
			for _, cancelCall := range []bool{false, true} {
				m := manager(t)
				o := options(target, "ssh", "exec")
				o.Jumps = []SSHJump{jumpOptions(addr, key)}
				o.Config = cfg(map[string]any{"connect_timeout_ms": 80})
				ctx := context.Background()
				if cancelCall {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
					defer cancel()
				}
				start := time.Now()
				if _, err := m.Connect(ctx, o); err == nil || time.Since(start) > time.Second {
					t.Fatal("unbounded jump connect", err, time.Since(start))
				}
				eventually(t, func() bool { return state.Active.Load() == 0 })
			}
		})
	}
}

func TestSSHJumpPrivateKeyAuthentication(t *testing.T) {
	target, _ := testutil.SSH(t)
	addr, key, _ := testutil.SSHJump(t, target, false)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(privateKey, "test jump", []byte("test-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jump_key")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	m := manager(t)
	o := options(target, "ssh", "exec")
	j := jumpOptions(addr, key)
	j.Password, j.Keyfile, j.KeyPassphrase = "", path, "test-passphrase"
	o.Jumps = []SSHJump{j}
	id := connect(t, m, o)
	if v := run(t, m, "exec", Request{Session: id, Command: "both"}); v.ExitCode == nil || *v.ExitCode != 0 {
		t.Fatal(v)
	}
}

func TestSSHJumpInvalidConfiguration(t *testing.T) {
	for _, o := range []Options{
		{Protocol: "telnet", Host: "localhost", Port: 23, Jumps: []SSHJump{{Host: "jump"}}},
		{Protocol: "ssh", Host: "localhost", Port: 22, Jumps: make([]SSHJump, 9)},
		{Protocol: "ssh", Host: "localhost", Port: 22, Jumps: []SSHJump{{Host: "jump", Username: "u"}}},
		{Protocol: "ssh", Host: "localhost", Port: 22, Jumps: []SSHJump{{Host: "jump", Username: "u", Password: "p", Port: -1}}},
	} {
		m := manager(t)
		if _, err := m.Connect(context.Background(), o); err == nil || len(m.List()) != 0 {
			t.Fatal("invalid jump accepted", err)
		}
	}
}

func TestSSHJumpDisconnectLeavesOutcomeUnknownAndReconnectable(t *testing.T) {
	target, _ := testutil.SSH(t)
	addr, key, state := testutil.SSHJump(t, target, false)
	m := manager(t)
	o := options(target, "ssh", "exec")
	o.Jumps = []SSHJump{jumpOptions(addr, key)}
	id := connect(t, m, o)
	v := run(t, m, "exec", Request{Session: id, Command: "hang", CollectOutput: noCollect()})
	handle := v.Handle
	s, _ := m.Get(id)
	_ = s.tcp.Close()
	eventually(t, func() bool { return !s.status().Connected })
	v = run(t, m, "task_status", Request{Handle: handle})
	// Channel EOF and transport closure can arrive in either order. Missing
	// exit-status may finish the channel first, but must keep its outcome unknown.
	unknown := v.State == "unknown" || (v.State == "finished" && v.ExitCode == nil && v.Warning != "")
	if !unknown || v.NextAction != "sess_reconnect" || v.Connected {
		t.Fatal(v)
	}
	if v, err := m.Reconnect(context.Background(), id); err != nil || !v.Connected {
		t.Fatal(v, err)
	}
	// Reconnect establishes only the chain, never the prior hang command.
	if tasks, err := m.TaskList(id); err != nil || len(tasks) != 0 {
		t.Fatal(tasks, err)
	}
	m.CloseAll()
	eventually(t, func() bool { return state.Active.Load() == 0 })
}

func TestCloseAllCancelsPendingMultiHopConnect(t *testing.T) {
	target, _ := testutil.SSH(t)
	second, secondKey, blocked := testutil.SSHJump(t, target, true)
	first, firstKey, outer := testutil.SSHJump(t, second, false)
	m := manager(t)
	o := options(target, "ssh", "exec")
	o.Jumps = []SSHJump{jumpOptions(first, firstKey), jumpOptions(second, secondKey)}
	finished := make(chan error, 1)
	go func() { _, err := m.Connect(context.Background(), o); finished <- err }()
	eventually(t, func() bool { return blocked.Forwards.Load() == 1 })
	m.CloseAll()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled connect succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("multi-hop connect ignored close_all cancellation")
	}
	if len(m.List()) != 0 {
		t.Fatal("cancelled connect resurrected a handle")
	}
	eventually(t, func() bool { return blocked.Active.Load() == 0 && outer.Active.Load() == 0 })
}

func TestSSHSecondJumpAuthenticationFailureClosesChain(t *testing.T) {
	target, _ := testutil.SSH(t)
	second, secondKey, inner := testutil.SSHJump(t, target, false)
	first, firstKey, outer := testutil.SSHJump(t, second, false)
	m := manager(t)
	o := options(target, "ssh", "exec")
	o.Jumps = []SSHJump{jumpOptions(first, firstKey), jumpOptions(second, secondKey)}
	o.Jumps[1].Password = "bad-second-hop-secret"
	_, err := m.Connect(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "SSH jump 2") || strings.Contains(err.Error(), "bad-second-hop-secret") {
		t.Fatal(err)
	}
	eventually(t, func() bool { return inner.Active.Load() == 0 && outer.Active.Load() == 0 })
	if inner.Forwards.Load() != 0 || len(m.List()) != 0 {
		t.Fatal("authentication failure forwarded application traffic or retained a session")
	}
}
