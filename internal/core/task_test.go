package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func taskServer(t *testing.T) (string, <-chan string) {
	t.Helper()
	signals := make(chan string, 128)
	addr, _ := testutil.SSHWithExec(t, func(command string, ch ssh.Channel, reqs <-chan *ssh.Request) {
		switch command {
		case "silent", "terminate":
			for req := range reqs {
				if req.Type == "signal" {
					var msg struct{ Signal string }
					if ssh.Unmarshal(req.Payload, &msg) != nil {
						return
					}
					signals <- command + ":" + msg.Signal
					if command == "terminate" || msg.Signal == "KILL" {
						_, _ = io.WriteString(ch.Stderr(), "shutdown\n")
						_, _ = ch.SendRequest("exit-signal", false, ssh.Marshal(struct {
							Signal            string
							Core              bool
							Message, Language string
						}{msg.Signal, false, "", ""}))
						return
					}
				}
			}
			return
		case "empty":
		case "flood":
			_, _ = io.WriteString(ch, strings.Repeat("x", HistoryLimit+1024))
		default:
			_, _ = io.WriteString(ch, command+" stdout\n")
			_, _ = io.WriteString(ch.Stderr(), command+" stderr\n")
		}
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
	})
	return addr, signals
}

func eventually(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTaskSyncSlotAndAsyncParallelism(t *testing.T) {
	addr, signals := taskServer(t)
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	a := run(t, m, "exec", Request{Session: id, Command: "silent", CollectOutput: noCollect()})
	if a.Handle != "task1" || a.State != "running" || a.Comment != "" {
		t.Fatal(a)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() { v, _ := m.Operate(ctx, "exec", Request{Session: id, Command: "silent"}); done <- v }()
	eventually(t, func() bool {
		ss, _ := m.TaskList(id)
		return len(ss) == 2 && ss[1].State == "running"
	})
	busy := run(t, m, "exec", Request{Session: id, Command: "must-not-run"})
	if busy.InputState != "not_sent" || !strings.Contains(busy.Error, "busy") || busy.Handle != "" {
		t.Fatal(busy)
	}
	b := run(t, m, "exec", Request{Session: id, Command: "other", CollectOutput: noCollect()})
	if b.InputState != "sent" || b.Handle == "" {
		t.Fatal(b)
	}
	v := run(t, m, "task_wait", Request{Handle: b.Handle})
	if v.Reason != "exec_finish" || !strings.Contains(v.Output, "other stdout") || !strings.Contains(v.Output, "other stderr") {
		t.Fatal(v)
	}
	cancel()
	select {
	case v = <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not release sync call")
	}
	if v.Reason != "cancelled" || v.Handle == "" || v.Signal != nil || !v.Running {
		t.Fatal(v)
	}
	v = run(t, m, "exec", Request{Session: id, Command: "empty"})
	if v.Reason != "exec_finish" || v.Handle != "" || v.Comment == "" {
		t.Fatal(v)
	}
	// An ignored TERM must not retain the synchronous slot.
	v = run(t, m, "exec", Request{Session: id, Command: "silent", Config: cfg(map[string]any{"exec_total_timeout_ms": 20})})
	if v.Reason != "total_timeout" || v.Signal == nil || v.Signal.RequestState != "sent" || !v.Running || v.Handle == "" {
		t.Fatal(v)
	}
	select {
	case signal := <-signals:
		if signal != "silent:TERM" {
			t.Fatal(signal)
		}
	case <-time.After(time.Second):
		t.Fatal("TERM missing")
	}
	v = run(t, m, "exec", Request{Session: id, Command: "empty"})
	if v.Reason != "exec_finish" {
		t.Fatal("sync slot retained after timeout", v)
	}
	if _, err := m.Operate(context.Background(), "read_available", Request{Session: id}); err == nil {
		t.Fatal("ambiguous session read accepted")
	}
}

func TestTaskSignalsWaitReadOnlyAndNoReadTermination(t *testing.T) {
	addr, signals := taskServer(t)
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	a := run(t, m, "exec", Request{Session: id, Command: "terminate", CollectOutput: noCollect(), Config: cfg(map[string]any{"exec_total_timeout_ms": 1})})
	v := run(t, m, "read_until", Request{Handle: a.Handle, Config: cfg(map[string]any{"read_total_timeout_ms": 10, "read_idle_timeout_ms": 0})})
	if v.Reason != "total_timeout" || v.Comment == "" || v.Signal != nil || !v.Running {
		t.Fatal(v)
	}
	v = run(t, m, "task_wait", Request{Handle: a.Handle, Config: cfg(map[string]any{"exec_total_timeout_ms": 10, "term_on_timeout": false})})
	if v.Signal != nil || !v.Running {
		t.Fatal(v)
	}
	if _, err := m.Configure(id, cfg(map[string]any{"read_only": true})); err != nil {
		t.Fatal(err)
	}
	v = run(t, m, "task_wait", Request{Handle: a.Handle, Config: cfg(map[string]any{"exec_total_timeout_ms": 10})})
	if v.Signal == nil || v.Signal.RequestState != "skipped_read_only" || !v.Running {
		t.Fatal(v)
	}
	v = run(t, m, "task_signal", Request{Handle: a.Handle, Signal: "TERM"})
	if !strings.HasPrefix(v.Error, "read_only:") {
		t.Fatal(v)
	}
	select {
	case v := <-signals:
		t.Fatal("unexpected signal", v)
	default:
	}
	if _, err := m.Configure(id, cfg(map[string]any{"read_only": false})); err != nil {
		t.Fatal(err)
	}
	v = run(t, m, "task_wait", Request{Handle: a.Handle, Config: cfg(map[string]any{"exec_total_timeout_ms": 10})})
	if v.Reason != "total_timeout" || v.Signal == nil || v.Signal.RequestState != "sent" {
		t.Fatal(v)
	}
	v = run(t, m, "task_wait", Request{Handle: a.Handle})
	if v.State != "finished" || v.ExitSignal != "TERM" || v.ExitCode != nil || v.Output != "shutdown\n" {
		t.Fatal(v)
	}
	status := run(t, m, "task_status", Request{Handle: a.Handle})
	if status.Output != "" || status.More || status.ExitSignal != "TERM" {
		t.Fatal(status)
	}
	v = run(t, m, "task_signal", Request{Handle: a.Handle, Signal: "KILL"})
	if v.Signal.RequestState != "skipped_finished" {
		t.Fatal(v)
	}
	if _, err := m.Operate(context.Background(), "task_signal", Request{Handle: a.Handle, Signal: "SIGTERM"}); err == nil {
		t.Fatal("invalid signal accepted")
	}
	if v = run(t, m, "task_release", Request{Handle: a.Handle}); v.Reason != "released" {
		t.Fatal(v)
	}
	if _, err := m.Operate(context.Background(), "task_status", Request{Handle: a.Handle}); err == nil {
		t.Fatal("released handle survived")
	}
	info, _ := m.GetConfig(id)
	if !info.Config.TermOnTimeout || info.Config.ExecTotalTimeoutMS != 120000 {
		t.Fatal("override leaked", info)
	}
}

func TestTaskWaitDoesNotBlockSignalOrSyncExec(t *testing.T) {
	addr, _ := taskServer(t)
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	a := run(t, m, "exec", Request{Session: id, Command: "terminate", CollectOutput: noCollect()})
	done := make(chan Result, 1)
	go func() { v, _ := m.Operate(context.Background(), "task_wait", Request{Handle: a.Handle}); done <- v }()
	s, _ := m.Get(id)
	eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.active > 0 })
	if _, err := m.Operate(context.Background(), "read_available", Request{Handle: a.Handle}); err == nil {
		t.Fatal("two readers accepted")
	}
	v := run(t, m, "exec", Request{Session: id, Command: "empty"})
	if v.Reason != "exec_finish" {
		t.Fatal(v)
	}
	v = run(t, m, "task_signal", Request{Handle: a.Handle, Signal: "INT"})
	if v.Signal.RequestState != "sent" {
		t.Fatal(v)
	}
	select {
	case v = <-done:
		if v.ExitSignal != "INT" || v.Output != "shutdown\n" {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("signal blocked behind wait")
	}
}

func TestTaskOutputIsolationAndParallelStarts(t *testing.T) {
	gate := make(chan struct{})
	addr, _ := testutil.SSHWithExec(t, func(command string, ch ssh.Channel, reqs <-chan *ssh.Request) {
		_, _ = io.WriteString(ch, command+"-out\n")
		<-gate
		_, _ = io.WriteString(ch.Stderr(), command+"-err\n")
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{7}))
	})
	var once sync.Once
	defer once.Do(func() { close(gate) })
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	results := make(chan Result, MaxRunningTasks)
	for i := 0; i < MaxRunningTasks; i++ {
		go func(i int) {
			v, _ := m.Operate(context.Background(), "exec", Request{Session: id, Command: fmt.Sprintf("cmd%d", i), CollectOutput: noCollect()})
			results <- v
		}(i)
	}
	var tasks []Result
	for i := 0; i < MaxRunningTasks; i++ {
		select {
		case v := <-results:
			if v.Handle == "" || v.InputState != "sent" {
				t.Fatal(v)
			}
			tasks = append(tasks, v)
		case <-time.After(2 * time.Second):
			t.Fatal("parallel startup blocked")
		}
	}
	v := run(t, m, "exec", Request{Session: id, Command: "over-limit", CollectOutput: noCollect()})
	if v.InputState != "not_sent" || !strings.Contains(v.Error, "resource_limit") || v.Handle != "" {
		t.Fatal(v)
	}
	once.Do(func() { close(gate) })
	seen := map[string]bool{}
	for _, task := range tasks {
		v = run(t, m, "task_wait", Request{Handle: task.Handle})
		if v.ExitCode == nil || *v.ExitCode != 7 || v.More {
			t.Fatal(v)
		}
		lines := strings.Fields(v.Output)
		sort.Strings(lines)
		if len(lines) != 2 || strings.TrimSuffix(lines[0], "-err") != strings.TrimSuffix(lines[1], "-out") || seen[lines[0]] {
			t.Fatal("mixed task output", v)
		}
		seen[lines[0]] = true
		history := run(t, m, "read_history", Request{Handle: task.Handle})
		if history.Output != v.Output {
			t.Fatal(history, v)
		}
		if empty := run(t, m, "read_available", Request{Handle: task.Handle}); empty.Output != "" || empty.Comment == "" {
			t.Fatal(empty)
		}
	}
}

func TestTaskRetentionCloseReconnectAndNoSilenceExpiry(t *testing.T) {
	addr, _ := taskServer(t)
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	a := run(t, m, "exec", Request{Session: id, Command: "silent", CollectOutput: noCollect()})
	s, _ := m.Get(id)
	s.mu.Lock()
	s.last, s.lastUse = time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)
	s.tasks[a.Handle].out.mu.Lock()
	s.tasks[a.Handle].out.last = s.last
	s.tasks[a.Handle].out.mu.Unlock()
	s.mu.Unlock()
	if s.expire(time.Now()) || !s.status().Connected {
		t.Fatal("silent background task reclaimed")
	}
	if _, err := m.Reconnect(context.Background(), id); err == nil {
		t.Fatal("reconnected with live task")
	}
	if _, err := m.Operate(context.Background(), "task_release", Request{Handle: a.Handle}); err == nil {
		t.Fatal("released running task")
	}
	for i := 0; i < MaxTasks+2; i++ {
		run(t, m, "exec", Request{Session: id, Command: "empty"})
	}
	list, _ := m.TaskList(id)
	if len(list) != MaxTasks {
		t.Fatal("unbounded history", len(list))
	}
	if _, err := m.Operate(context.Background(), "task_status", Request{Handle: "task2"}); err == nil {
		t.Fatal("oldest completed task not evicted")
	}
	if v := run(t, m, "task_status", Request{Handle: a.Handle}); v.State != "running" {
		t.Fatal("running task evicted", v)
	}
	s.mu.Lock()
	for _, task := range s.tasks {
		task.out.mu.Lock()
		if task.out.completed {
			task.finished = time.Now().Add(-TaskRetention - time.Second)
		}
		task.out.mu.Unlock()
	}
	s.mu.Unlock()
	list, _ = m.TaskList(id)
	if len(list) != 1 {
		t.Fatal("completed task TTL ignored", list)
	}
	run(t, m, "task_signal", Request{Handle: a.Handle, Signal: "KILL"})
	run(t, m, "task_wait", Request{Handle: a.Handle})
	if v, err := m.Reconnect(context.Background(), id); err != nil || !v.Connected {
		t.Fatal(v, err)
	}
	if _, err := m.Operate(context.Background(), "task_status", Request{Handle: a.Handle}); err == nil {
		t.Fatal("handle survived reconnect")
	}
	b := run(t, m, "exec", Request{Session: id, Command: "silent", CollectOutput: noCollect()})
	if b.Handle == a.Handle {
		t.Fatal("handle reused")
	}
	done := make(chan Result, 1)
	go func() { v, _ := m.Operate(context.Background(), "task_wait", Request{Handle: b.Handle}); done <- v }()
	s, _ = m.Get(id)
	eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.active > 0 })
	m.CloseAll()
	select {
	case v := <-done:
		if v.Reason != "closed" || v.Handle != b.Handle {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wake waiter")
	}
	if list, _ := m.TaskList(""); len(list) != 0 {
		t.Fatal(list)
	}
}

func TestTaskHistoryIsBoundedAndReportsLoss(t *testing.T) {
	addr, _ := taskServer(t)
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	a := run(t, m, "exec", Request{Session: id, Command: "flood", CollectOutput: noCollect()})
	eventually(t, func() bool { v := run(t, m, "task_status", Request{Handle: a.Handle}); return v.State == "finished" })
	v := run(t, m, "read_history", Request{Handle: a.Handle})
	if v.DroppedBytes != 1024 || !v.More || len(v.Output) != 65536 {
		t.Fatal(v.DroppedBytes, v.More, len(v.Output))
	}
	v = run(t, m, "read_available", Request{Handle: a.Handle})
	if v.DroppedBytes != 1024 || v.Start != 1024 {
		t.Fatal(v.DroppedBytes, v.Start)
	}
	s, _ := m.Get(id)
	s.mu.Lock()
	task := s.tasks[a.Handle]
	s.mu.Unlock()
	task.out.mu.Lock()
	retained := len(task.out.buf)
	task.out.mu.Unlock()
	if retained > HistoryLimit {
		t.Fatal(retained)
	}
}

type fakeExecChannel struct {
	startFn  func(string, *Session) error
	waitFn   func() error
	signalFn func(ssh.Signal) error
	closeFn  func() error
}

func (f *fakeExecChannel) start(cmd string, out *Session) error { return f.startFn(cmd, out) }
func (f *fakeExecChannel) Wait() error                          { return f.waitFn() }
func (f *fakeExecChannel) Signal(sig ssh.Signal) error          { return f.signalFn(sig) }
func (f *fakeExecChannel) Close() error {
	if f.closeFn != nil {
		return f.closeFn()
	}
	return nil
}

func TestTaskSignalFailureStillReleasesSyncSlot(t *testing.T) {
	m, s, _ := localSession(t)
	s.mode = "exec"
	s.newExec = func() (execChannel, error) {
		return &fakeExecChannel{
			startFn:  func(string, *Session) error { return nil },
			waitFn:   func() error { <-s.done; return errors.New("closed") },
			signalFn: func(ssh.Signal) error { return errors.New("signal rejected") },
		}, nil
	}
	for i := 0; i < 2; i++ {
		v := run(t, m, "exec", Request{Session: s.id, Command: "ignored", Config: cfg(map[string]any{"exec_total_timeout_ms": 10})})
		if v.InputState != "sent" || v.Reason != "total_timeout" || v.Signal == nil || v.Signal.RequestState != "unknown" || !v.Connected {
			t.Fatal(v)
		}
	}
}

func TestTaskSignalWriteGuardAndDeadline(t *testing.T) {
	m, s, _ := localSession(t)
	s.mode = "exec"
	entered := make(chan struct{})
	s.newExec = func() (execChannel, error) {
		return &fakeExecChannel{
			startFn:  func(string, *Session) error { return nil },
			waitFn:   func() error { <-s.done; return errors.New("closed") },
			signalFn: func(ssh.Signal) error { close(entered); <-s.done; return errors.New("closed") },
		}, nil
	}
	a := run(t, m, "exec", Request{Session: s.id, Command: "ignored", CollectOutput: noCollect()})
	done := make(chan Result, 1)
	go func() {
		v, _ := m.Operate(context.Background(), "task_signal", Request{Handle: a.Handle, Signal: "TERM"})
		done <- v
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("signal did not start")
	}
	before, _ := m.GetConfig(s.id)
	if _, err := m.Configure(s.id, cfg(map[string]any{"read_only": true, "max_output_bytes": 1})); err == nil {
		t.Fatal("guard changed during signal write")
	}
	after, _ := m.GetConfig(s.id)
	if before.Config != after.Config {
		t.Fatal("busy config partially applied")
	}
	select {
	case v := <-done:
		if v.Signal.RequestState != "unknown" || v.Connected || v.State != "unknown" {
			t.Fatal(v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("signal exceeded deadline")
	}
	if _, err := m.Operate(context.Background(), "task_release", Request{Handle: a.Handle}); err == nil {
		t.Fatal("unknown task released as finished")
	}
}

func TestTaskSetupTimeoutAndCloseDoNotResurrectHandle(t *testing.T) {
	for _, closeAll := range []bool{false, true} {
		t.Run(fmt.Sprint(closeAll), func(t *testing.T) {
			m, s, _ := localSession(t)
			s.mode = "exec"
			entered := make(chan struct{})
			s.newExec = func() (execChannel, error) { close(entered); <-s.done; return nil, errors.New("connection closed") }
			done := make(chan Result, 1)
			go func() {
				v, _ := m.Operate(context.Background(), "exec", Request{Session: s.id, Command: "ignored", Config: cfg(map[string]any{"write_timeout_ms": 30})})
				done <- v
			}()
			<-entered
			if closeAll {
				m.CloseAll()
			}
			select {
			case v := <-done:
				if v.InputState != "unknown" && v.InputState != "not_sent" {
					t.Fatal(v)
				}
			case <-time.After(time.Second):
				t.Fatal("setup not bounded")
			}
			if closeAll {
				list, _ := m.TaskList("")
				if len(list) != 0 {
					t.Fatal("resurrected task", list)
				}
			}
		})
	}
}

func TestEmptyOutputCommentsMatchEvidence(t *testing.T) {
	m, s, _ := localSession(t)
	v := run(t, m, "send_text", Request{Session: s.id, Data: "x", CollectOutput: noCollect()})
	if v.Comment != "" {
		t.Fatal("send-only receipt claimed empty device output", v)
	}
	v = run(t, m, "read_available", Request{Session: s.id})
	if !strings.Contains(v.Comment, "No new") {
		t.Fatal(v)
	}
	_, _ = s.Write([]byte{0xe4})
	v = run(t, m, "read_available", Request{Session: s.id})
	if !strings.Contains(v.Comment, "Partial") || v.Output != "" {
		t.Fatal(v)
	}
	_, _ = s.Write([]byte{0xb8, 0xad})
	v = run(t, m, "read_available", Request{Session: s.id, Config: cfg(map[string]any{"max_output_bytes": 1})})
	if !strings.Contains(v.Comment, "too small") || v.MinimumOutputBytes != 3 {
		t.Fatal(v)
	}
	v = run(t, m, "read_available", Request{Session: s.id})
	if v.Output != "中" || v.Comment != "" {
		t.Fatal(v)
	}
	s.disconnect("test transport failure")
	v = run(t, m, "read_available", Request{Session: s.id})
	if v.Comment == "" || strings.Contains(v.Comment, "call is valid") {
		t.Fatal(v)
	}
}

func TestGlobalTaskLimitsAndRejectedStartup(t *testing.T) {
	m := manager(t)
	for i := 0; i < MaxGlobalRunningTasks/MaxRunningTasks+1; i++ {
		id := fmt.Sprintf("s%d", i+1)
		s := &Session{id: id, protocol: "ssh", mode: "exec", config: DefaultConfig("ssh"), connected: true, last: time.Now(), lastUse: time.Now(), notify: make(chan struct{}, 1), done: make(chan struct{})}
		s.newExec = func() (execChannel, error) {
			return &fakeExecChannel{
				startFn:  func(string, *Session) error { return nil },
				waitFn:   func() error { <-s.done; return errors.New("closed") },
				signalFn: func(ssh.Signal) error { return nil },
			}, nil
		}
		m.sessions[id] = s
		for j := 0; j < MaxRunningTasks; j++ {
			v := run(t, m, "exec", Request{Session: id, Command: "background", CollectOutput: noCollect()})
			if i*MaxRunningTasks+j >= MaxGlobalRunningTasks {
				if v.InputState != "not_sent" || !strings.Contains(v.Error, "resource_limit") {
					t.Fatal(v)
				}
				break
			}
			if v.InputState != "sent" || v.Handle == "" {
				t.Fatal(v)
			}
		}
	}
	m.CloseAll()
	// A channel-open rejection is known not to have sent application input.
	s := &Session{id: "rejected", protocol: "ssh", mode: "exec", config: DefaultConfig("ssh"), connected: true, notify: make(chan struct{}, 1), done: make(chan struct{})}
	s.newExec = func() (execChannel, error) { return nil, errors.New("server channel limit") }
	m.sessions[s.id] = s
	v := run(t, m, "exec", Request{Session: s.id, Command: "never sent"})
	if v.InputState != "not_sent" || v.Handle != "" || !v.Connected {
		t.Fatal(v)
	}
	list, _ := m.TaskList(s.id)
	if len(list) != 0 {
		t.Fatal("rejected startup leaked record", list)
	}
}

func TestTaskShortCallerDeadlineIsNotShortenedBySignalReserve(t *testing.T) {
	addr, signals := taskServer(t)
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	a := run(t, m, "exec", Request{Session: id, Command: "silent", CollectOutput: noCollect()})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	v, err := m.Operate(ctx, "task_wait", Request{Handle: a.Handle})
	if err != nil || v.Reason != "total_timeout" || time.Since(start) < 40*time.Millisecond {
		t.Fatal(v, err, time.Since(start))
	}
	select {
	case signal := <-signals:
		t.Fatal("external deadline caused premature signal", signal)
	default:
	}
}
