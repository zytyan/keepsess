package core

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func TestNamesStableIDsAndConcurrentTaskReservations(t *testing.T) {
	addr, _ := taskServer(t)
	m := manager(t)
	o := options(addr, "ssh", "exec")
	o.Name = "prod"
	v, err := m.Connect(context.Background(), o)
	if err != nil || v.Name != "prod" || !regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{6,9}$`).MatchString(v.InstanceID) {
		t.Fatal(v, err)
	}
	id := v.Session
	if _, err = m.Connect(context.Background(), o); err == nil {
		t.Fatal("duplicate session name connected")
	}
	if v, err = m.RenameSession("prod", "router"); err != nil || v.Session != id || v.Name != "router" {
		t.Fatal(v, err)
	}
	if _, err = m.Get("prod"); err == nil {
		t.Fatal("old alias still resolves")
	}
	if conf, err := m.GetConfig("router"); err != nil || conf.Session != id {
		t.Fatal(conf, err)
	}
	var wg sync.WaitGroup
	var sent atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := m.Operate(context.Background(), "exec", Request{Session: "router", Name: "build", Command: "silent", CollectOutput: noCollect()})
			if err != nil {
				t.Error(err)
				return
			}
			if v.InputState == "sent" {
				sent.Add(1)
			} else if !strings.Contains(v.Error, "duplicate task name") {
				t.Errorf("unexpected rejection: %+v", v)
			}
		}()
	}
	wg.Wait()
	if sent.Load() != 1 {
		t.Fatal("duplicate named tasks launched", sent.Load())
	}
	v = run(t, m, "task_status", Request{Handle: "build"})
	handle := v.Handle
	if v.Name != "build" || v.Session != id || handle == "" {
		t.Fatal(v)
	}
	if v, err = m.RenameTask("build", "compile"); err != nil || v.Handle != handle {
		t.Fatal(v, err)
	}
	if _, err = m.Operate(context.Background(), "task_status", Request{Handle: "build"}); err == nil {
		t.Fatal("old task alias resolves")
	}
	if v = run(t, m, "task_status", Request{Handle: "compile"}); v.Handle != handle {
		t.Fatal(v)
	}
	if _, err = m.RenameTask(handle, "task999"); err == nil {
		t.Fatal("reserved task ID accepted")
	}
	if _, err = m.RenameSession(id, "s999"); err == nil {
		t.Fatal("reserved session ID accepted")
	}
	if tasks, err := m.TaskList("router"); err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	_, err = m.Get("missing")
	var lookup *SessionLookupError
	if !errors.As(err, &lookup) || lookup.InstanceID != m.InstanceID() || !strings.Contains(err.Error(), "instance changed") {
		t.Fatal(err)
	}
	if _, err = m.Close("router"); err != nil {
		t.Fatal(err)
	}
}

func TestPendingSessionNameReservedBeforeDial(t *testing.T) {
	var connections atomic.Int32
	addr := testutil.Listen(t, func(c net.Conn) { connections.Add(1); _, _ = io.Copy(io.Discard, c) })
	m := manager(t)
	o := options(addr, "ssh", "exec")
	o.Name = "pending"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.Connect(ctx, o); done <- err }()
	eventually(t, func() bool { return connections.Load() == 1 })
	if _, err := m.Connect(context.Background(), o); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatal(err)
	}
	if connections.Load() != 1 {
		t.Fatal("duplicate name opened a TCP connection")
	}
	cancel()
	<-done
}

func TestWaitAfterRPCBudgetGetsNewOutputAndCompletion(t *testing.T) {
	var executions atomic.Int32
	addr, _ := testutil.SSHWithExec(t, func(_ string, ch ssh.Channel, reqs <-chan *ssh.Request) {
		executions.Add(1)
		go ssh.DiscardRequests(reqs)
		_, _ = io.WriteString(ch, "first\n")
		time.Sleep(120 * time.Millisecond)
		_, _ = io.WriteString(ch, "second\n")
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
	})
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	a := run(t, m, "exec", Request{Session: id, Command: "progress", CollectOutput: noCollect()})
	task, err := m.acquireTask(a.Handle)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), callDeadlineKey{}, time.Now().Add(SignalLimit+50*time.Millisecond+25*time.Millisecond))
	task.out.readMu.Lock()
	v := task.wait(ctx, DefaultConfig("ssh"))
	task.out.readMu.Unlock()
	task.out.touch(-1)
	task.session.touch(-1)
	if v.TimeoutSource != "rpc_budget" || (v.WaitElapsedMS == nil || *v.WaitElapsedMS < 15) || v.Signal != nil || v.Handle != a.Handle {
		t.Fatal(v)
	}
	v = run(t, m, "task_wait", Request{Handle: a.Handle, Config: cfg(map[string]any{"exec_total_timeout_ms": 1000, "term_on_timeout": false})})
	if v.Reason != "exec_finish" || !strings.Contains(v.Output, "second") || v.TimeoutSource != "" || v.TaskElapsedMS < 100 || (v.WaitElapsedMS == nil || *v.WaitElapsedMS < 30) || executions.Load() != 1 {
		t.Fatal(v)
	}
	status := run(t, m, "task_status", Request{Handle: a.Handle})
	time.Sleep(5 * time.Millisecond)
	if next := run(t, m, "task_status", Request{Handle: a.Handle}); next.TaskElapsedMS != status.TaskElapsedMS {
		t.Fatal("completed elapsed time kept increasing", status, next)
	}
	// A binding-owned RPC deadline must also be distinguished when TERM reserve
	// is disabled; Operate must preserve that boundary instead of adding 115s.
	a = run(t, m, "exec", Request{Session: id, Command: "progress", CollectOutput: noCollect()})
	bound, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	v, err = m.Operate(WithRPCBudget(bound), "task_wait", Request{Handle: a.Handle, Config: cfg(map[string]any{"term_on_timeout": false})})
	if err != nil || v.TimeoutSource != "rpc_budget" || v.Signal != nil {
		t.Fatal(v, err)
	}
	v = run(t, m, "task_wait", Request{Handle: a.Handle, Config: cfg(map[string]any{"exec_total_timeout_ms": 1000, "term_on_timeout": false})})
	if v.Reason != "exec_finish" || !strings.Contains(v.Output, "second") {
		t.Fatal(v)
	}
}

func TestExecBashLoginCwdEnvAndNoLoginRaw(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("Bash unavailable on this test host")
	}
	commands := make(chan string, 8)
	addr, _ := testutil.SSHWithExec(t, func(command string, ch ssh.Channel, reqs <-chan *ssh.Request) {
		commands <- command
		go ssh.DiscardRequests(reqs)
		cmd := exec.Command("bash", "-c", command)
		cmd.Stdin = ch
		cmd.Stdout = ch
		cmd.Stderr = ch.Stderr()
		err := cmd.Run()
		code := 0
		if err != nil {
			code = 1
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			}
		}
		var payload [4]byte
		binary.BigEndian.PutUint32(payload[:], uint32(code))
		_, _ = ch.SendRequest("exit-status", false, payload[:])
	})
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	dir := filepath.Join(t.TempDir(), "space ' quoted")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	value := "a '$() ; \\ value"
	v := run(t, m, "exec", Request{Session: id, Command: `printf '%s\n' "$TEST_VALUE"; pwd; shopt -q login_shell`, Cwd: dir, Env: map[string]string{"TEST_VALUE": value}})
	if v.ExitCode == nil || *v.ExitCode != 0 || !strings.Contains(v.Output, value) || !strings.Contains(v.Output, dir) {
		t.Fatal(v)
	}
	if command := <-commands; !strings.HasPrefix(command, "bash --login -c '") {
		t.Fatal(command)
	}
	no := false
	v = run(t, m, "exec", Request{Session: id, Login: &no, Command: "printf raw"})
	if command := <-commands; command != "printf raw" {
		t.Fatal(command)
	}
	if v.Output != "raw" || v.ExitCode == nil || *v.ExitCode != 0 {
		t.Fatal(v)
	}
	v = run(t, m, "exec", Request{Session: id, Cwd: filepath.Join(dir, "missing"), Command: "printf must-not-run"})
	<-commands
	if strings.Contains(v.Output, "must-not-run") || v.ExitCode == nil || *v.ExitCode == 0 {
		t.Fatal(v)
	}
	if _, err := m.Operate(context.Background(), "exec", Request{Session: id, Env: map[string]string{"BAD-NAME": "x"}, Command: "x"}); err == nil {
		t.Fatal("invalid env was sent")
	}
}
