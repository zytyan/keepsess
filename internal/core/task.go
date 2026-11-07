package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	MaxRunningTasks       = 8 // Per connection, including synchronous commands.
	MaxTasks              = 16
	MaxGlobalRunningTasks = 64
	MaxGlobalTasks        = 128
	TaskRetention         = 30 * time.Minute
	SignalLimit           = time.Second
)

type SignalResult struct {
	Name         string `json:"name"`
	RequestState string `json:"request_state"`
	Error        string `json:"error,omitempty"`
}

type callDeadlineKey struct{}

// WithRPCBudget marks a binding-owned deadline, so an RPC limit is reported
// separately from a shorter caller-imposed observation deadline.
func WithRPCBudget(ctx context.Context) context.Context {
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithValue(ctx, callDeadlineKey{}, deadline)
	}
	return ctx
}

type execChannel interface {
	start(string, *Session) error
	Wait() error
	Signal(ssh.Signal) error
	Close() error
}

// Lock order: Manager.mu -> connection.mu -> task.out.mu. No task lock may
// acquire a connection lock. Control writes share writeMu; guard changes take
// it exclusively. A reader pins its task until the call returns.
type Task struct {
	session  *Session
	out      *Session
	channel  execChannel // Protected by out.mu.
	finished time.Time   // Protected by out.mu.
	sequence uint64
	signalMu sync.Mutex
	inputMu  sync.Mutex
	stdin    io.WriteCloser // Published under out.mu before state=running; inputMu serializes writes.
}

func (s *Session) runningLocked() bool {
	if s.running {
		return true
	}
	for _, t := range s.tasks {
		t.out.mu.Lock()
		live := !t.out.completed && t.out.connected && !t.out.disposed
		t.out.mu.Unlock()
		if live {
			return true
		}
	}
	return false
}

func (s *Session) discardTasksLocked() {
	for _, t := range s.tasks {
		t.out.mu.Lock()
		t.out.disposed = true
		t.out.buf = nil
		t.out.base, t.out.cursor = t.out.total, t.out.total
		t.out.mu.Unlock()
		t.out.disconnect("task discarded by connection close/reconnect; remote outcome may be unknown")
	}
	s.tasks = nil
}

func (s *Session) pruneTasksLocked(now time.Time) {
	for handle, t := range s.tasks {
		t.out.mu.Lock()
		if t.out.completed && t.out.active == 0 && now.Sub(t.finished) >= TaskRetention {
			t.out.disposed = true
			t.out.buf = nil
			delete(s.tasks, handle)
		}
		t.out.mu.Unlock()
	}
}

// Evict only completed, unpinned records. Unknown outcomes are never silently
// treated as completed; explicit connection close/reconnect releases them.
func evictTaskLocked(s *Session, t *Task) {
	t.out.mu.Lock()
	t.out.disposed = true
	t.out.buf = nil
	delete(s.tasks, t.out.handle)
	t.out.mu.Unlock()
}

func (m *Manager) allocateTask(s *Session, c Config, interactive, pty bool, name string) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[s.id] != s {
		return nil, errors.New("session disposed; nothing sent")
	}
	total, running, localTotal, localRunning := 0, 0, 0, 0
	var oldest, localOldest *Task
	for _, conn := range m.sessions {
		conn.mu.Lock()
		conn.pruneTasksLocked(time.Now())
		for _, t := range conn.tasks {
			total++
			if conn == s {
				localTotal++
			}
			t.out.mu.Lock()
			if name != "" && t.out.name == name {
				t.out.mu.Unlock()
				conn.mu.Unlock()
				return nil, errors.New("duplicate task name; nothing sent")
			}
			if !t.out.completed {
				running++
				if conn == s {
					localRunning++
				}
			} else if t.out.active == 0 {
				if oldest == nil || t.sequence < oldest.sequence {
					oldest = t
				}
				if conn == s && (localOldest == nil || t.sequence < localOldest.sequence) {
					localOldest = t
				}
			}
			t.out.mu.Unlock()
		}
		conn.mu.Unlock()
	}
	if running >= MaxGlobalRunningTasks || localRunning >= MaxRunningTasks {
		return nil, errors.New("resource_limit: running task limit reached; nothing sent")
	}
	// Choose a candidate, then recheck the pin under its connection lock.
	var victim *Task
	if localTotal >= MaxTasks {
		victim = localOldest
	} else if total >= MaxGlobalTasks {
		victim = oldest
	}
	if localTotal >= MaxTasks || total >= MaxGlobalTasks {
		if victim == nil {
			return nil, errors.New("resource_limit: retained task limit reached; release completed tasks; nothing sent")
		}
		conn := victim.session
		conn.mu.Lock()
		victim.out.mu.Lock()
		busy := victim.out.active != 0
		victim.out.mu.Unlock()
		if busy {
			conn.mu.Unlock()
			return nil, errors.New("resource_limit: retained tasks busy; nothing sent")
		}
		evictTaskLocked(conn, victim)
		conn.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disposed || !s.connected || s.reconnecting {
		return nil, errors.New("connection unavailable; nothing sent")
	}
	m.nextTask++
	now := time.Now()
	t := &Task{session: s, sequence: m.nextTask, out: &Session{
		id: s.id, name: name, handle: fmt.Sprintf("task%d", m.nextTask), protocol: "ssh", mode: "exec", state: "starting",
		config: c, interactive: interactive, pty: pty, connected: true, running: true, active: 1, last: now, lastUse: now,
		notify: make(chan struct{}, 1), done: make(chan struct{}),
	}}
	if s.tasks == nil {
		s.tasks = make(map[string]*Task)
	}
	t.out.readMu.Lock() // Startup owns collection before the handle is discoverable.
	s.tasks[t.out.handle] = t
	return t, nil
}

func (m *Manager) startTask(ctx context.Context, s *Session, r Request, c Config) (Result, error) {
	if r.PTY && !r.Interactive {
		return Result{}, errors.New("pty=true requires interactive=true; nothing sent")
	}
	if r.Command == "" || r.Data != "" {
		return Result{}, errors.New("command required; data is not accepted")
	}
	if err := validateName(r.Name); err != nil {
		return Result{}, err
	}
	command, err := execCommand(r)
	if err != nil {
		return Result{}, err
	}
	b, err := encodeInput(command, false, c.Encoding)
	if err != nil {
		return Result{}, err
	}
	if int64(len(b)) > c.MaxInputBytes {
		return Result{}, fmt.Errorf("input exceeds max_input_bytes=%d; nothing sent", c.MaxInputBytes)
	}
	collect := r.CollectOutput == nil || *r.CollectOutput
	if collect {
		if !s.execMu.TryLock() {
			v := s.status()
			v.InputState = "not_sent"
			v.Error = "busy: synchronous exec call in progress; nothing sent"
			return v, nil
		}
		defer s.execMu.Unlock() // Release even when TERM is ignored or fails.
	}
	if !s.writeMu.TryRLock() {
		return Result{}, errors.New("busy: configuration/write/reconnect in progress; nothing sent")
	}
	locked := true
	defer func() {
		if locked {
			s.writeMu.RUnlock()
		}
	}()
	s.mu.Lock()
	if s.disposed || s.reconnecting {
		s.mu.Unlock()
		return Result{}, errors.New("session unavailable; nothing sent")
	}
	s.active++
	s.lastUse = time.Now()
	readOnly := s.config.ReadOnly
	s.mu.Unlock()
	defer s.touch(-1)
	v := s.status()
	v.InputState, v.InputBytes = "not_sent", len(b)
	if readOnly {
		v.Error = "read_only: application input disabled; nothing sent"
		return v, nil
	}
	if !v.Connected {
		v.Reason = "closed"
		return v, nil
	}
	if ctx.Err() != nil {
		v.Reason = "cancelled"
		return v, nil
	}
	if c.BackpressureBytes > 0 {
		n, err := queuedBytes(s.tcp)
		if err != nil {
			v.Error = err.Error()
			return v, nil
		}
		v.QueuedBytes = &n
		if int64(n) >= c.BackpressureBytes {
			v.Error = "backpressure: TCP queue at threshold; nothing sent"
			return v, nil
		}
	}
	t, err := m.allocateTask(s, c, r.Interactive, r.PTY, r.Name)
	if err != nil {
		v.Error = err.Error()
		return v, nil
	}
	defer t.out.touch(-1)
	// Pin the initial collector before publishing any command output.
	defer t.out.readMu.Unlock()
	v = t.start(ctx, string(b), c)
	s.writeMu.RUnlock()
	locked = false
	if v.InputState == "not_sent" {
		s.mu.Lock()
		evictTaskLocked(s, t)
		s.mu.Unlock()
		v.Handle = ""
		return v, nil
	}
	if v.InputState != "sent" || !collect {
		return v, nil
	}
	result := t.wait(ctx, c)
	result.InputState, result.InputBytes, result.WrittenBytes = v.InputState, v.InputBytes, v.WrittenBytes
	if result.Reason == "exec_finish" && !result.More {
		result.Handle = ""
	}
	return result, nil
}

func (t *Task) start(ctx context.Context, command string, c Config) Result {
	type startup struct {
		err       error
		attempted bool
	}
	done := make(chan startup, 1)
	go func() {
		ch, err := t.session.newExec()
		if err != nil {
			done <- startup{err: err}
			return
		}
		t.out.mu.Lock()
		if t.out.disposed || !t.out.connected {
			t.out.mu.Unlock()
			done <- startup{err: errors.New("connection closed before exec request")}
			t.closeChannel(ch)
			return
		}
		t.channel = ch
		t.out.mu.Unlock()
		if t.out.interactive {
			input, ok := ch.(interface {
				prepareInput(bool) (io.WriteCloser, error)
			})
			if !ok {
				err = errors.New("exec channel does not support interactive stdin")
			} else {
				var stdin io.WriteCloser
				stdin, err = input.prepareInput(t.out.pty)
				if err == nil {
					t.out.mu.Lock()
					t.stdin = stdin
					t.out.stdinOpen = true
					t.out.mu.Unlock()
				}
			}
			if err != nil {
				done <- startup{err: err}
				t.closeChannel(ch)
				return
			}
		}
		err = ch.start(command, t.out)
		if err != nil {
			done <- startup{err: err, attempted: true}
			t.closeChannel(ch)
			return
		}
		t.out.mu.Lock()
		if t.out.connected && !t.out.disposed {
			t.out.state = "running"
			if t.out.startedAt.IsZero() {
				t.out.startedAt = time.Now()
			}
		}
		t.out.mu.Unlock()
		done <- startup{attempted: true}
		t.finish(ch.Wait())
	}()
	timer := time.NewTimer(c.Duration(c.WriteTimeoutMS))
	defer timer.Stop()
	var result startup
	select {
	case result = <-done:
	case <-timer.C:
		result = startup{errors.New("exec setup timeout; shared transport closed; outcome may be unknown"), true}
		t.session.disconnect(result.err.Error())
	case <-ctx.Done():
		result = startup{ctx.Err(), true}
		t.session.disconnect("exec setup cancelled; shared transport closed; outcome may be unknown")
	}
	v := t.out.status()
	v.InputBytes = len(command)
	if result.err != nil {
		t.out.mu.Lock()
		t.out.state, t.out.running, t.out.failure = "unknown", false, result.err.Error()
		t.out.mu.Unlock()
		v = t.out.status()
		v.InputBytes = len(command)
		v.Reason, v.InputState = "aborted", "unknown"
		if !result.attempted {
			v.InputState = "not_sent"
		}
		if errors.Is(result.err, context.Canceled) {
			v.Reason = "cancelled"
		}
		return v
	}
	v.InputState, v.WrittenBytes = "sent", len(command)
	return v
}

func (t *Task) finish(err error) {
	t.out.mu.Lock()
	defer t.out.mu.Unlock()
	if t.out.disposed {
		return
	}
	t.out.stdinOpen = false
	var exit *ssh.ExitError
	var missing *ssh.ExitMissingError
	switch {
	case err == nil:
		code := 0
		t.out.exit = &code
	case errors.As(err, &exit):
		t.out.exitSignal = exit.Signal()
		// ssh synthesizes a status for exit-signal. Do not present it as a
		// server-provided exit code when only an exit signal is available.
		if exit.Signal() == "" {
			code := exit.ExitStatus()
			t.out.exit = &code
		}
	case errors.As(err, &missing):
		if !t.out.connected {
			t.out.state = "unknown"
			t.out.signal()
			return
		}
		t.out.warning = "SSH channel closed without exit-status/exit-signal; command outcome is unknown"
	default:
		t.out.state, t.out.running, t.out.failure = "unknown", false, fmt.Sprintf("SSH task error: %v; inspect connection and do not replay", err)
		t.out.signal()
		return
	}
	t.out.state, t.out.running, t.out.completed = "finished", false, true
	t.out.stdinOpen = false
	t.finished = time.Now()
	t.out.finishedAt = t.finished
	t.out.signal()
}

// A stuck SSH control write can only be interrupted reliably by closing the
// shared transport. This failure is explicit and updates every task.
func (t *Task) closeChannel(ch execChannel) {
	done := make(chan struct{})
	go func() { _ = ch.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(SignalLimit):
		t.session.disconnect("SSH channel close stalled; shared transport closed; reconnect and inspect tasks")
	}
}

func (t *Task) wait(ctx context.Context, c Config) Result {
	started := time.Now()
	// Reserve control-write time inside the 115s RPC budget. Parent cancellation
	// still cancels observation without terminating the command.
	completionDeadline := time.Now().Add(c.Duration(c.ExecTotalTimeoutMS))
	waitCtx := ctx
	if c.TermOnTimeout {
		if deadline, ok := ctx.Value(callDeadlineKey{}).(time.Time); ok {
			var cancel context.CancelFunc
			waitCtx, cancel = context.WithDeadline(ctx, deadline.Add(-SignalLimit-50*time.Millisecond))
			defer cancel()
		}
	}
	v := t.out.read(waitCtx, "exec_wait", Request{}, c)
	elapsed := time.Since(started).Milliseconds()
	v.WaitElapsedMS = &elapsed
	if v.Reason == "total_timeout" {
		if waitCtx.Err() == nil {
			v.TimeoutSource = "exec_wait"
		} else if ctx.Err() == nil {
			v.TimeoutSource = "rpc_budget"
		} else {
			v.TimeoutSource = "caller_deadline"
			if deadline, ok := ctx.Value(callDeadlineKey{}).(time.Time); ok && !time.Now().Before(deadline) {
				v.TimeoutSource = "rpc_budget"
			}
		}
	}
	if v.Reason == "total_timeout" && v.TimeoutSource == "caller_deadline" {
		v.Comment = "Caller deadline ended this wait; task retained. Continue with a new wait/read call. No TERM sent for caller deadline expiry."
	}
	if v.Reason == "total_timeout" && v.TimeoutSource == "rpc_budget" {
		v.Comment = "RPC collection budget reached; task retained. Continue with sess_task_wait or handle-based read. No TERM sent for RPC budget expiry."
	}
	if v.Reason == "total_timeout" && !time.Now().Before(completionDeadline) && c.TermOnTimeout && waitCtx.Err() == nil && ctx.Err() == nil {
		v.Signal = t.sendSignal(ctx, "TERM")
		latest := t.out.status()
		v.State, v.Running, v.Connected, v.More = latest.State, latest.Running, latest.Connected, latest.More
		v.TaskElapsedMS = latest.TaskElapsedMS
		v.ExitCode, v.ExitSignal, v.Warning, v.NextAction = latest.ExitCode, latest.ExitSignal, latest.Warning, latest.NextAction
		v.DisconnectReason = latest.DisconnectReason
		if latest.Error != "" {
			v.Error = latest.Error
		}
		if v.Output == "" && !v.Connected {
			v.Comment = "No output was returned. The signal attempt encountered a connection failure; inspect signal and task state. Remote outcome may be unknown."
		}
	}
	return v
}

func validSignal(name string) bool {
	switch name {
	case "ABRT", "ALRM", "FPE", "HUP", "ILL", "INT", "KILL", "PIPE", "QUIT", "SEGV", "TERM", "USR1", "USR2":
		return true
	}
	return false
}

func (t *Task) sendSignal(ctx context.Context, name string) *SignalResult {
	r := &SignalResult{Name: name, RequestState: "not_sent"}
	if !t.signalMu.TryLock() {
		r.Error = "busy: signal request in progress"
		return r
	}
	defer t.signalMu.Unlock()
	defer func() { t.out.mu.Lock(); t.out.lastSignal = r; t.out.mu.Unlock() }()
	s := t.session
	if !s.writeMu.TryRLock() {
		r.Error = "busy: configure or reconnect in progress"
		return r
	}
	defer s.writeMu.RUnlock()
	s.mu.Lock()
	guard, disposed := s.config.ReadOnly, s.disposed || s.reconnecting
	s.mu.Unlock()
	if guard {
		r.RequestState = "skipped_read_only"
		r.Error = "read_only: signals disabled"
		return r
	}
	if disposed {
		r.Error = "session unavailable"
		return r
	}
	t.out.mu.Lock()
	ch, state, connected, disposed := t.channel, t.out.state, t.out.connected, t.out.disposed
	t.out.mu.Unlock()
	if state == "finished" {
		r.RequestState = "skipped_finished"
		return r
	}
	if ch == nil || state != "running" || !connected || disposed {
		r.Error = "task not available for signals"
		return r
	}
	if ctx.Err() != nil {
		r.Error = ctx.Err().Error()
		return r
	}
	done := make(chan error, 1)
	go func() { done <- ch.Signal(ssh.Signal(name)) }()
	timer := time.NewTimer(SignalLimit)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			r.RequestState, r.Error = "unknown", err.Error()
		} else {
			r.RequestState = "sent"
		}
	case <-timer.C:
		r.RequestState, r.Error = "unknown", "signal write timeout; shared transport closed; remote outcome unknown"
		s.disconnect(r.Error)
	case <-ctx.Done():
		r.RequestState, r.Error = "unknown", "signal write cancelled; shared transport closed; remote outcome unknown"
		s.disconnect(r.Error)
	}
	return r
}

// acquireTask pins both task and connection, preventing eviction and reconnect
// for the entire operation. Close remains immediate and invalidates both.
func (m *Manager) acquireTask(handle string) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		s.mu.Lock()
		s.pruneTasksLocked(time.Now())
		t := s.tasks[handle]
		if t == nil {
			for _, candidate := range s.tasks {
				candidate.out.mu.Lock()
				match := candidate.out.name == handle
				candidate.out.mu.Unlock()
				if match {
					t = candidate
					break
				}
			}
		}
		if t != nil && !s.disposed && !s.reconnecting {
			t.out.mu.Lock()
			t.out.active++
			t.out.lastUse = time.Now()
			t.out.mu.Unlock()
			s.active++
			s.lastUse = time.Now()
			s.mu.Unlock()
			return t, nil
		}
		s.mu.Unlock()
	}
	return nil, &TaskLookupError{Handle: handle, InstanceID: m.instanceID}
}

func (m *Manager) TaskList(session string) ([]Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session = m.resolveSessionLocked(session)
	if session != "" && m.sessions[session] == nil {
		return nil, m.sessionErrorLocked(session)
	}
	out := make([]Result, 0)
	for _, s := range m.sessions {
		if session != "" && s.id != session {
			continue
		}
		s.mu.Lock()
		s.pruneTasksLocked(time.Now())
		if !s.disposed && !s.reconnecting {
			s.lastUse = time.Now()
			for _, t := range s.tasks {
				out = append(out, t.out.status())
			}
		}
		s.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out, nil
}

func (m *Manager) operateTask(ctx context.Context, kind string, r Request) (Result, error) {
	if r.Handle == "" || r.Session != "" {
		return Result{}, errors.New("provide handle only; session and handle are mutually exclusive")
	}
	if strings.HasPrefix(kind, "send") || kind == "task_close_stdin" {
		return m.taskInput(ctx, kind, r)
	}
	if r.Interactive || r.PTY {
		return Result{}, errors.New("interactive/pty are only for sess_exec")
	}
	if r.Data != "" || r.Command != "" || r.CollectOutput != nil {
		return Result{}, errors.New("task/read operations do not accept input")
	}
	switch kind {
	case "read_available", "read_until", "read_history", "read_tail", "task_status", "task_wait", "task_signal", "task_release":
	default:
		return Result{}, errors.New("handle is only for task/read tools")
	}
	if r.Offset != nil && (kind != "read_history" || *r.Offset < 0) {
		return Result{}, errors.New("nonnegative offset is only for read_history")
	}
	if r.Lines < 0 || r.Lines > 100000 || (r.Lines != 0 && kind != "read_tail") {
		return Result{}, errors.New("lines 0..100000 is only for read_tail")
	}
	if kind == "task_signal" {
		if !validSignal(r.Signal) {
			return Result{}, errors.New("invalid signal; use TERM, INT, KILL or another supported SSH signal without SIG prefix")
		}
	} else if r.Signal != "" {
		return Result{}, errors.New("signal is only for task_signal")
	}
	if len(r.Config) != 0 && !strings.HasPrefix(kind, "read_") && kind != "task_wait" {
		return Result{}, errors.New("config is only for read/task_wait")
	}
	t, err := m.acquireTask(r.Handle)
	if err != nil {
		return Result{}, err
	}
	defer t.session.touch(-1)
	defer t.out.touch(-1)
	switch kind {
	case "task_status":
		return t.out.status(), nil
	case "task_signal":
		sig := t.sendSignal(ctx, r.Signal)
		v := t.out.status()
		v.Signal = sig
		if sig.Error != "" {
			v.Error = sig.Error
		}
		return v, nil
	case "task_release":
		s := t.session
		s.mu.Lock()
		defer s.mu.Unlock()
		t.out.mu.Lock()
		busy := !t.out.completed || t.out.active != 1
		t.out.mu.Unlock()
		if busy {
			return Result{}, errors.New("busy: task still running, outcome unknown or operation active; not released")
		}
		v := t.out.status()
		evictTaskLocked(s, t)
		v.Reason = "released"
		return v, nil
	}
	t.session.mu.Lock()
	c := t.session.config
	t.session.mu.Unlock()
	c, err = applyConfig(c, r.Config, true)
	if err != nil {
		return Result{}, err
	}
	if !t.out.readMu.TryLock() {
		return Result{}, errors.New("busy: another reader owns this task")
	}
	defer t.out.readMu.Unlock()
	if kind == "task_wait" {
		return t.wait(ctx, c), nil
	}
	return t.out.read(ctx, kind, r, c), nil
}
