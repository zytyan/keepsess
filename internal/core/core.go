// Package core owns bounded, process-local SSH exec and terminal streams.
package core

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	HistoryLimit  = 1 << 20
	ResultLimit   = 512 << 10
	MaxInputLimit = 1 << 20
	MaxSessions   = 32
	CallLimit     = 115 * time.Second
	IOLimit       = 10 * time.Second
)

type Options struct {
	Name          string      `json:"name,omitempty"`
	SSHJumps      []SSHJump   `json:"ssh_jumps,omitempty"`
	Protocol      string      `json:"protocol"`
	Host          string      `json:"host"`
	Port          int         `json:"port"`
	Username      string      `json:"username,omitempty"`
	Password      string      `json:"password,omitempty"`
	Keyfile       string      `json:"keyfile,omitempty"`
	KeyPassphrase string      `json:"key_passphrase,omitempty"`
	KnownHosts    string      `json:"known_hosts,omitempty"`
	HostKey       string      `json:"host_key,omitempty"`
	Insecure      bool        `json:"insecure,omitempty"`
	Mode          string      `json:"mode,omitempty"`
	Config        ConfigPatch `json:"config,omitempty"`
}

// SSHJump describes one forwarding hop, ordered from the local machine outward.
// Authentication is private session state, never part of a tool result.
type SSHJump struct {
	Host          string `json:"host"`
	Port          int    `json:"port,omitempty"`
	Username      string `json:"username"`
	Password      string `json:"password,omitempty"`
	Keyfile       string `json:"keyfile,omitempty"`
	KeyPassphrase string `json:"key_passphrase,omitempty"`
	KnownHosts    string `json:"known_hosts,omitempty"`
	HostKey       string `json:"host_key,omitempty"`
	Insecure      bool   `json:"insecure,omitempty"`
}

func (j SSHJump) options() Options {
	return Options{Host: j.Host, Port: j.Port, Username: j.Username, Password: j.Password,
		Keyfile: j.Keyfile, KeyPassphrase: j.KeyPassphrase, KnownHosts: j.KnownHosts,
		HostKey: j.HostKey, Insecure: j.Insecure}
}

func (o Options) address() string { return net.JoinHostPort(o.Host, strconv.Itoa(o.Port)) }

type Request struct {
	Name          string            `json:"name,omitempty"`
	Login         *bool             `json:"login,omitempty"`
	Cwd           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Interactive   bool              `json:"interactive,omitempty"`
	PTY           bool              `json:"pty,omitempty"`
	Session       string            `json:"session"`
	Handle        string            `json:"handle,omitempty"`
	Signal        string            `json:"signal,omitempty"`
	Data          string            `json:"data,omitempty"`
	Command       string            `json:"command,omitempty"`
	CollectOutput *bool             `json:"collect_output,omitempty"`
	Config        ConfigPatch       `json:"config,omitempty"`
	Lines         int               `json:"lines,omitempty"`
	Offset        *int64            `json:"offset,omitempty"`
	Columns       int               `json:"columns,omitempty"`
	Rows          int               `json:"rows,omitempty"`
}

// PeerIdentity is handshake metadata, emitted only on connect/reconnect.
type PeerIdentity struct {
	Server         string `json:"server"`
	HostKey        string `json:"host_key"`
	HostKeyChanged bool   `json:"host_key_changed,omitempty"`
}

// Result separates local write receipts from observed stream output.
type Result struct {
	Name               string        `json:"name,omitempty"`
	InstanceID         string        `json:"instance_id,omitempty"`
	InstanceName       string        `json:"instance_name,omitempty"`
	StartedAt          *time.Time    `json:"started_at,omitempty"`
	TaskElapsedMS      int64         `json:"task_elapsed_ms,omitempty"`
	WaitElapsedMS      *int64        `json:"wait_elapsed_ms,omitempty"`
	TimeoutSource      string        `json:"timeout_source,omitempty"`
	FilteredBytes      int64         `json:"filtered_bytes,omitempty"`
	FilteredLines      int64         `json:"filtered_lines,omitempty"`
	StrippedBytes      int64         `json:"stripped_bytes,omitempty"`
	LineFragments      bool          `json:"line_fragments,omitempty"`
	Interactive        bool          `json:"interactive,omitempty"`
	PTY                bool          `json:"pty,omitempty"`
	StdinOpen          *bool         `json:"stdin_open,omitempty"`
	Peer               *PeerIdentity `json:"peer,omitempty"`
	DisconnectReason   string        `json:"disconnect_reason,omitempty"`
	Session            string        `json:"session"`
	Handle             string        `json:"handle,omitempty"`
	State              string        `json:"state,omitempty"`
	Comment            string        `json:"comment,omitempty"`
	ExitSignal         string        `json:"exit_signal,omitempty"`
	Signal             *SignalResult `json:"signal,omitempty"`
	Output             string        `json:"output,omitempty"`
	Reason             string        `json:"reason,omitempty"`
	Connected          bool          `json:"connected"`
	Running            bool          `json:"running,omitempty"`
	InputState         string        `json:"input_state,omitempty"`
	InputBytes         int           `json:"input_bytes,omitempty"`
	WrittenBytes       int           `json:"written_bytes,omitempty"`
	ExitCode           *int          `json:"exit_code,omitempty"`
	Warning            string        `json:"warning,omitempty"`
	Error              string        `json:"error,omitempty"`
	NextAction         string        `json:"next_action,omitempty"`
	Start              int64         `json:"start,omitempty"`
	End                int64         `json:"end,omitempty"`
	More               bool          `json:"more,omitempty"`
	DroppedBytes       int64         `json:"dropped_bytes,omitempty"`
	MinimumOutputBytes int           `json:"minimum_output_bytes,omitempty"`
	QueuedBytes        *int          `json:"queued_bytes,omitempty"`
}
type Session struct {
	origin string // Internal Web task attribution, never inferred from command text.
	events EventSink
	epoch  uint64

	sshClient                               *ssh.Client
	tunnels                                 map[string]*Tunnel
	nextTunnel                              uint64
	name                                    string
	startedAt                               time.Time
	finishedAt                              time.Time
	id, protocol, mode                      string
	mu                                      sync.Mutex
	readMu, configMu, execMu                sync.Mutex
	writeMu                                 sync.RWMutex
	config                                  Config
	options                                 Options
	reconnecting                            bool
	buf                                     []byte
	base, total, cursor                     int64
	last, lastUse                           time.Time
	active                                  int
	connected, running, completed, disposed bool
	failure, warning                        string
	disconnectReason, retiredReason         string
	peer                                    *PeerIdentity
	keepaliveWake                           chan struct{}
	exit                                    *int
	notify, done                            chan struct{}
	once                                    sync.Once
	closer                                  io.Closer
	tcp                                     *net.TCPConn
	input                                   io.Writer
	newExec                                 func() (execChannel, error)
	tasks                                   map[string]*Task
	handle, state, exitSignal               string
	lastSignal                              *SignalResult
	interactive, pty, stdinOpen             bool
}
type Manager struct {
	events      EventSink      // Installed before serving; callbacks must never block.
	connections ConnectionSink // Private metadata, never emitted as public events.

	instanceID             string
	instanceName           string
	instanceNamingPrompted bool
	sessionNames           map[string]string
	mu                     sync.Mutex
	sessions               map[string]*Session
	nextID                 uint64
	nextTask               uint64
	opening                int
	generation             uint64
	pending                map[string]context.CancelFunc
	retired                map[string]retiredSession
}

func NewManager() *Manager {
	return &Manager{instanceID: newInstanceID(), sessionNames: make(map[string]string), sessions: make(map[string]*Session), pending: make(map[string]context.CancelFunc)}
}
func (m *Manager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id = m.resolveSessionLocked(id)
	s := m.sessions[id]
	if s == nil {
		return nil, m.sessionErrorLocked(id)
	}
	s.mu.Lock()
	disposed, reason := s.disposed, s.retiredReason
	s.mu.Unlock()
	if disposed {
		m.rememberLocked(id, reason)
		delete(m.sessions, id)
		return nil, m.sessionErrorLocked(id)
	}
	return s, nil
}

// uniqueExecSession selects among all retained sessions, including disconnected
// and stream sessions. Never infer a target from protocol, mode or connectivity.
func (m *Manager) uniqueExecSession() (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected *Session
	for id, s := range m.sessions {
		s.mu.Lock()
		disposed, reason := s.disposed, s.retiredReason
		s.mu.Unlock()
		if disposed {
			m.rememberLocked(id, reason)
			delete(m.sessions, id)
			continue
		}
		if selected != nil {
			return nil, errors.New("multiple retained sessions; specify session explicitly; use sess_list; nothing sent")
		}
		selected = s
	}
	if selected == nil {
		return nil, errors.New("no retained session; use sess_connect first; nothing sent")
	}
	return selected, nil
}
func (m *Manager) Connect(ctx context.Context, o Options) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, CallLimit)
	defer cancel()
	if o.Protocol != "ssh" && o.Protocol != "telnet" {
		return Result{}, errors.New("protocol must be ssh or telnet")
	}
	if o.Host == "" || o.Port < 1 || o.Port > 65535 {
		return Result{}, errors.New("host and port (1..65535) required")
	}
	if len(o.SSHJumps) > 8 {
		return Result{}, errors.New("ssh_jumps supports at most 8 SSH hops")
	}
	// Own the slice before defaulting or retaining it for explicit reconnect.
	o.SSHJumps = append([]SSHJump(nil), o.SSHJumps...)
	for i := range o.SSHJumps {
		j := &o.SSHJumps[i]
		if j.Port == 0 {
			j.Port = 22
		}
		if j.Host == "" || j.Port < 1 || j.Port > 65535 || j.Username == "" || (j.Password == "" && j.Keyfile == "") {
			return Result{}, fmt.Errorf("SSH jump %d requires host, port 1..65535 (default 22), username and password or keyfile", i+1)
		}
	}
	if o.Mode == "" {
		o.Mode = "exec"
		if o.Protocol == "telnet" {
			o.Mode = "stream"
		}
	}
	if (o.Mode != "stream" && o.Mode != "exec") || (o.Protocol == "telnet" && o.Mode != "stream") {
		return Result{}, errors.New("mode must be stream or exec; Telnet requires stream")
	}
	if err := validateName(o.Name); err != nil {
		return Result{}, err
	}
	cfg, err := applyConfig(DefaultConfig(o.Protocol), o.Config, false)
	if err != nil {
		return Result{}, err
	}
	m.mu.Lock()
	if len(m.sessions)+m.opening >= MaxSessions {
		m.mu.Unlock()
		return Result{}, errors.New("session limit reached; close unused sessions")
	}
	m.resolveSessionLocked("")
	if o.Name != "" && m.sessionNames[o.Name] != "" {
		m.mu.Unlock()
		return Result{}, errors.New("duplicate session name; nothing sent")
	}
	m.opening++
	m.nextID++
	id := fmt.Sprintf("s%d", m.nextID)
	if o.Name != "" {
		m.sessionNames[o.Name] = id
	}
	generation := m.generation
	m.pending[id] = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.opening--
		delete(m.pending, id)
		if m.sessions[id] == nil && m.sessionNames[o.Name] == id {
			delete(m.sessionNames, o.Name)
		}
		m.mu.Unlock()
	}()
	ctx, stop := context.WithTimeout(ctx, cfg.Duration(cfg.ConnectTimeoutMS))
	defer stop()
	s := &Session{id: id, name: o.Name, protocol: o.Protocol, mode: o.Mode, config: cfg, last: time.Now(), lastUse: time.Now(), connected: true, notify: make(chan struct{}, 1), done: make(chan struct{})}
	s.events, s.epoch = m.events, 1
	s.record("connecting", nil, "", "", nil)
	s.options = o
	s.options.Config = nil // Retain connection credentials privately, not a caller-owned config map.
	if o.Protocol == "ssh" {
		err = s.connectSSH(ctx, o)
	} else {
		err = s.connectTelnet(ctx, o)
	}
	if err != nil {
		s.disconnect(err.Error())
		return Result{}, err
	}
	m.mu.Lock()
	if generation != m.generation {
		m.mu.Unlock()
		s.disconnect("")
		return Result{}, errors.New("connect cancelled by sess_close_all")
	}
	m.sessions[id] = s
	promptName := !m.instanceNamingPrompted && m.instanceName == ""
	m.instanceNamingPrompted = true
	instanceName := m.instanceName
	connectionWarning := m.recordConnectionLocked(s, o)
	m.mu.Unlock()
	s.record("session", nil, "", "", nil)
	go m.watch(s)
	v := s.status()
	v.Warning = connectionWarning
	s.mu.Lock()
	v.Peer = s.peer
	v.InstanceID = m.instanceID
	v.InstanceName = instanceName
	s.mu.Unlock()
	if promptName {
		v.Comment = "First connection succeeded. Name this instance for the current work using sess_configure({\"instance_name\":\"short task label\"}); instance_id stays unchanged. Do not reconnect or replay commands."
		v.NextAction = "sess_configure"
	}
	return v, nil
}
func (s *Session) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}
func (s *Session) Write(p []byte) (int, error) { return s.writeOutput(p, "") }
func (s *Session) writeOutput(p []byte, source string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disposed {
		return len(p), nil
	}
	s.recordSourceLocked("output", p, "", "", nil, source)
	s.last = time.Now()
	s.total += int64(len(p))
	if len(p) >= HistoryLimit {
		s.buf = append(s.buf[:0], p[len(p)-HistoryLimit:]...)
	} else {
		s.buf = append(s.buf, p...)
		if len(s.buf) > HistoryLimit {
			s.buf = append([]byte(nil), s.buf[len(s.buf)-HistoryLimit:]...)
		}
	}
	s.base = s.total - int64(len(s.buf))
	s.signal()
	return len(p), nil
}
func (s *Session) disconnect(why string) {
	s.disconnectWith(classifyDisconnect(nil, why), why)
}
func (s *Session) disconnectWith(code, why string) {
	s.once.Do(func() {
		s.mu.Lock()
		s.connected = false
		s.running = false
		s.failure = why
		s.disconnectReason = code
		c := s.closer
		for _, task := range s.tasks {
			task.out.disconnectWith(code, why)
		}
		if s.handle != "" && !s.completed {
			s.state = "unknown"
		}
		s.recordLocked("session", nil, "", "", nil)
		s.mu.Unlock()
		close(s.done)
		s.signal()
		if c != nil {
			_ = c.Close()
		}
	})
}
func (s *Session) status() Result { s.mu.Lock(); defer s.mu.Unlock(); return s.statusLocked() }
func (s *Session) statusLocked() Result {
	r := Result{Name: s.name, Session: s.id, Handle: s.handle, State: s.state, ExitSignal: s.exitSignal, Signal: s.lastSignal, Connected: s.connected && !s.disposed, Running: s.runningLocked(), ExitCode: s.exit, Warning: s.warning, Error: s.failure, DisconnectReason: s.disconnectReason}
	if !s.startedAt.IsZero() {
		started := s.startedAt
		r.StartedAt = &started
		end := time.Now()
		if !s.finishedAt.IsZero() {
			end = s.finishedAt
		}
		r.TaskElapsedMS = end.Sub(started).Milliseconds()
	}
	if s.handle != "" && s.interactive {
		r.Interactive = true
		r.PTY = s.pty
		open := s.stdinOpen && s.connected && !s.completed && !s.disposed
		r.StdinOpen = &open
	}
	if s.disposed && s.retiredReason != "" {
		r.DisconnectReason = s.retiredReason
	}
	if s.handle != "" {
		r.More = s.cursor < s.total
	}
	if !s.connected {
		r.NextAction = "sess_reconnect"
		if s.disposed {
			r.NextAction = "sess_connect"
		}
	}
	return r
}
func (s *Session) touch(delta int) {
	s.mu.Lock()
	s.active += delta
	s.lastUse = time.Now()
	s.mu.Unlock()
}
func (s *Session) expire(now time.Time) bool {
	s.mu.Lock()
	s.pruneTasksLocked(now)
	unused := s.active == 0 && !s.runningLocked() && s.config.SessionIdleTimeoutMS > 0 && now.Sub(s.lastUse) >= s.config.Duration(s.config.SessionIdleTimeoutMS)
	if unused {
		s.disposed = true
		s.retiredReason = "session_idle_expired"
		s.recordLocked("session", nil, "", "", nil)
		s.options = Options{}
		s.buf = nil
		s.base = s.total
		s.cursor = s.total
		s.discardTasksLocked()
	}
	s.mu.Unlock()
	if unused {
		s.disconnect("")
	}
	return unused
}
func (m *Manager) watch(s *Session) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		s.expire(time.Now())
		s.mu.Lock()
		disposed := s.disposed
		reason := s.retiredReason
		s.mu.Unlock()
		if disposed {
			m.mu.Lock()
			if m.sessions[s.id] == s {
				m.rememberLocked(s.id, reason)
				delete(m.sessions, s.id)
			}
			m.mu.Unlock()
			return
		}
	}
}

func (m *Manager) Close(id string) (Result, error) {
	m.mu.Lock()
	id = m.resolveSessionLocked(id)
	s := m.sessions[id]
	if s == nil {
		err := m.sessionErrorLocked(id)
		m.mu.Unlock()
		return Result{}, err
	}
	m.rememberLocked(id, "explicit_close")
	delete(m.sessions, id)
	if cancel := m.pending[id]; cancel != nil {
		cancel()
	}
	m.mu.Unlock()
	s.mu.Lock()
	s.disposed = true
	s.retiredReason = "explicit_close"
	s.recordLocked("session", nil, "", "", nil)
	s.options = Options{}
	s.buf = nil
	s.base = s.total
	s.cursor = s.total
	s.discardTasksLocked()
	s.mu.Unlock()
	s.disconnect("")
	return Result{Session: id, Reason: "closed", Connected: false}, nil
}
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ss := m.sessions
	for id := range ss {
		m.rememberLocked(id, "explicit_close")
	}
	m.sessions = make(map[string]*Session)
	m.sessionNames = make(map[string]string)
	m.generation++
	for _, cancel := range m.pending {
		cancel()
	}
	m.mu.Unlock()
	for _, s := range ss {
		s.mu.Lock()
		s.disposed = true
		s.retiredReason = "explicit_close"
		s.recordLocked("session", nil, "", "", nil)
		s.options = Options{}
		s.buf = nil
		s.base = s.total
		s.cursor = s.total
		s.discardTasksLocked()
		s.mu.Unlock()
		s.disconnect("")
	}
}
func (m *Manager) List() []Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Result, 0, len(m.sessions))
	for id, s := range m.sessions {
		s.mu.Lock()
		disposed := s.disposed
		reason := s.retiredReason
		s.mu.Unlock()
		if disposed {
			m.rememberLocked(id, reason)
			delete(m.sessions, id)
			continue
		}
		out = append(out, s.status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Session < out[j].Session })
	return out
}
func (m *Manager) GetConfig(id string) (ConfigResult, error) {
	s, err := m.Get(id)
	if err != nil {
		return ConfigResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disposed {
		return ConfigResult{}, errors.New("session disposed; use sess_connect")
	}
	s.lastUse = time.Now()
	return configInfo(s.id, s.config), nil
}
func (m *Manager) Configure(id string, p ConfigPatch) (ConfigResult, error) {
	s, err := m.Get(id)
	if err != nil {
		return ConfigResult{}, err
	}
	if !s.configMu.TryLock() {
		return ConfigResult{}, errors.New("busy: configure or reconnect in progress")
	}
	defer s.configMu.Unlock()
	// Serialize guard changes with application writes, without an unbounded wait.
	// A successful configure guarantees any preceding write has finished.
	if _, changingGuard := p["read_only"]; changingGuard {
		if !s.writeMu.TryLock() {
			return ConfigResult{}, errors.New("busy: application write in progress; read_only unchanged; retry sess_configure after it finishes")
		}
		defer s.writeMu.Unlock()
	}
	s.mu.Lock()
	old := s.config
	connected := s.connected
	disposed := s.disposed
	s.mu.Unlock()
	if disposed {
		return ConfigResult{}, errors.New("session disposed")
	}
	cfg, err := applyConfig(old, p, false)
	if err != nil {
		return ConfigResult{}, err
	}
	if connected && cfg.TCPKeepaliveMS != old.TCPKeepaliveMS {
		if err = s.setKeepalive(cfg.TCPKeepaliveMS); err != nil {
			if restore := s.setKeepalive(old.TCPKeepaliveMS); restore != nil {
				s.disconnect("TCP keepalive configuration failed; reconnect")
				return ConfigResult{}, errors.Join(err, restore)
			}
			return ConfigResult{}, err
		}
	}
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return ConfigResult{}, errors.New("session disposed")
	}
	s.config = cfg
	s.recordLocked("session", nil, "", "", nil)
	if s.keepaliveWake != nil {
		select {
		case s.keepaliveWake <- struct{}{}:
		default:
		}
	}
	s.lastUse = time.Now()
	s.mu.Unlock()
	return ConfigResult{Session: s.id, Config: cfg}, nil
}
func (s *Session) setKeepalive(ms int64) error {
	if s.tcp == nil {
		return errors.New("TCP keepalive unavailable")
	}
	if err := s.tcp.SetKeepAlive(ms > 0); err != nil {
		return err
	}
	if ms > 0 {
		return s.tcp.SetKeepAlivePeriod(time.Duration(ms) * time.Millisecond)
	}
	return nil
}
func (m *Manager) Operate(parent context.Context, kind string, r Request) (Result, error) {
	deadline := time.Now().Add(CallLimit)
	if inherited, ok := parent.Value(callDeadlineKey{}).(time.Time); ok && inherited.Before(deadline) {
		deadline = inherited
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	ctx = context.WithValue(ctx, callDeadlineKey{}, deadline)
	if strings.HasPrefix(kind, "read_") && r.Session != "" && r.Handle != "" {
		return Result{}, errors.New("exec task reads require handle only: remove session; stream reads require session only: remove handle")
	}
	if (strings.HasPrefix(kind, "send") || strings.HasPrefix(kind, "read_")) && r.Handle == "" && r.Session == "" {
		return Result{}, errors.New("provide exactly one of session (stream) or handle (exec task)")
	}
	if kind != "exec" && (r.Login != nil || r.Cwd != "" || r.Env != nil || r.Name != "") {
		return Result{}, errors.New("name/login/cwd/env are exec startup options only")
	}
	if r.Handle != "" || strings.HasPrefix(kind, "task_") {
		return m.operateTask(ctx, kind, r)
	}
	if kind != "exec" && (r.Interactive || r.PTY) {
		return Result{}, errors.New("interactive/pty are only for sess_exec")
	}
	if r.Signal != "" {
		return Result{}, errors.New("signal is only for sess_task_signal")
	}
	var s *Session
	var err error
	if kind == "exec" && r.Session == "" {
		s, err = m.uniqueExecSession()
	} else {
		s, err = m.Get(r.Session)
	}
	if err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	cfg := s.config
	disposed := s.disposed
	reconnecting := s.reconnecting
	s.mu.Unlock()
	if reconnecting {
		return Result{}, errors.New("busy: reconnect in progress; nothing sent")
	}
	if disposed {
		return Result{}, errors.New("session disposed; use sess_connect")
	}
	if kind == "exec" && (s.mode != "exec" || s.protocol != "ssh") {
		return Result{}, errors.New("sess_exec requires SSH mode=exec; stream sessions do not support exec; nothing sent")
	}
	cfg, err = applyConfig(cfg, r.Config, true)
	if err != nil {
		return Result{}, err
	}
	if r.Offset != nil && *r.Offset < 0 {
		return Result{}, errors.New("offset must be nonnegative")
	}
	if r.Lines < 0 || r.Lines > 100000 {
		return Result{}, errors.New("lines must be 0..100000")
	}
	switch kind {
	case "exec", "send_text", "send_escaped", "sendline_text", "sendline_escaped", "read_available", "read_until", "read_history", "read_tail":
	default:
		return Result{}, errors.New("unknown operation")
	}
	sending := kind == "exec" || strings.HasPrefix(kind, "send")
	if !sending && (r.Data != "" || r.Command != "" || r.CollectOutput != nil) {
		return Result{}, errors.New("read tools do not accept input")
	}
	if kind != "read_history" && r.Offset != nil {
		return Result{}, errors.New("offset is only for read_history")
	}
	if kind != "read_tail" && r.Lines != 0 {
		return Result{}, errors.New("lines is only for read_tail")
	}
	if kind == "exec" {
		return m.startTask(ctx, s, r, cfg)
	}
	if !sending && s.mode == "exec" {
		return Result{}, errors.New("exec output requires handle; use sess_task_list to find retained tasks")
	}
	if sending && cfg.ReadOnly {
		v := s.status()
		v.InputState = "not_sent"
		v.Error = "read_only: application input disabled; nothing sent; change explicitly with sess_configure"
		return v, nil
	}
	collect := !sending || r.CollectOutput == nil || *r.CollectOutput
	if collect {
		if !s.readMu.TryLock() {
			return Result{}, errors.New("busy: another read owns this session; wait for it")
		}
		defer s.readMu.Unlock()
	}
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return Result{}, errors.New("session disposed; use sess_connect")
	}
	s.active++
	s.lastUse = time.Now()
	s.mu.Unlock()
	defer s.touch(-1)
	if !sending {
		return s.read(ctx, kind, r, cfg), nil
	}
	if s.mode != "stream" {
		return Result{}, errors.New("send tools require a stream session or an interactive exec task handle; target the task handle, do not replay exec; nothing sent")
	}
	if r.Command != "" {
		return Result{}, errors.New("send tools accept data, not command")
	}
	payload := r.Data
	b, err := encodeInput(payload, strings.HasSuffix(kind, "_escaped"), cfg.Encoding)
	if err != nil {
		return Result{}, err
	}
	if strings.HasPrefix(kind, "sendline_") {
		end, _ := encodeInput(cfg.LineEnd, true, "utf-8")
		b = append(b, end...)
	}
	if int64(len(b)) > cfg.MaxInputBytes {
		return Result{}, fmt.Errorf("input exceeds max_input_bytes=%d; nothing sent", cfg.MaxInputBytes)
	}
	if !s.writeMu.TryLock() {
		return Result{}, errors.New("busy: another write owns this session; nothing sent")
	}
	result := s.write(ctx, kind, b, cfg)
	s.recordInput(b, cfg.Encoding, &result)
	s.writeMu.Unlock()
	if result.InputState != "sent" || !collect {
		return result, nil
	}
	waitKind := "read_until"
	v := s.read(ctx, waitKind, Request{Session: r.Session}, cfg)
	v.InputState = result.InputState
	v.InputBytes = result.InputBytes
	v.WrittenBytes = result.WrittenBytes
	return v, nil
}
func (s *Session) write(ctx context.Context, kind string, b []byte, c Config) Result {
	v := s.status()
	v.InputState = "not_sent"
	v.InputBytes = len(b)
	if !v.Connected {
		v.Reason = "closed"
		if v.Error != "" {
			v.Reason = "aborted"
		}
		return v
	}
	s.mu.Lock()
	readOnly := s.config.ReadOnly
	s.mu.Unlock()
	// Recheck the live guard while holding writeMu: an operation may have
	// captured its config before a concurrent sess_configure enabled it.
	if readOnly {
		v.Error = "read_only: application input disabled; nothing sent; change explicitly with sess_configure"
		return v
	}
	if c.BackpressureBytes > 0 {
		if s.tcp == nil {
			v.Error = "TCP queue query unavailable"
			return v
		}
		n, err := queuedBytes(s.tcp)
		if err != nil {
			v.Error = err.Error()
			return v
		}
		v.QueuedBytes = &n
		if int64(n) >= c.BackpressureBytes {
			v.Error = "backpressure: TCP queue at threshold; nothing sent"
			return v
		}
	}
	if err := ctx.Err(); err != nil {
		v.Reason = "cancelled"
		return v
	}
	type written struct {
		n   int
		err error
	}
	done := make(chan written, 1)
	go func() {
		var n int
		var err error
		if writer, ok := s.input.(interface {
			WriteWithTimeout([]byte, time.Duration) (int, error)
		}); ok {
			n, err = writer.WriteWithTimeout(b, c.Duration(c.WriteTimeoutMS))
		} else {
			n, err = s.input.Write(b)
		}
		if n != len(b) && err == nil {
			err = io.ErrShortWrite
		}
		done <- written{n, err}
	}()
	timer := time.NewTimer(c.Duration(c.WriteTimeoutMS))
	defer timer.Stop()
	var w written
	select {
	case w = <-done:
	case <-timer.C:
		w.err = errors.New("write/setup timeout; input may have executed")
	case <-ctx.Done():
		w.err = ctx.Err()
	}
	if w.err != nil {
		s.disconnectError(w.err, "write failed")
		v = s.status()
		v.InputState = "unknown"
		v.InputBytes = len(b)
		v.WrittenBytes = w.n
		v.Reason = "aborted"
		if errors.Is(w.err, context.Canceled) {
			v.Reason = "cancelled"
		}
		return v
	}
	v = s.status()
	v.InputState = "sent"
	v.InputBytes = len(b)
	v.WrittenBytes = w.n
	return v
}

func (s *Session) keepalivePeriod() time.Duration {
	if s.config.TCPKeepaliveMS == 0 {
		return -1
	}
	return s.config.Duration(s.config.TCPKeepaliveMS)
}

// Reconnect replaces the transport instance, never reusing old readers or buffers.
// Connection parameters remain private; no application input is replayed.
func (m *Manager) Reconnect(parent context.Context, id string) (Result, error) {
	ctx, cancel := context.WithTimeout(parent, CallLimit)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	old, err := m.Get(id)
	if err != nil {
		return Result{}, err
	}
	id = old.id
	if !old.configMu.TryLock() {
		return Result{}, errors.New("busy: configure or reconnect in progress")
	}
	defer old.configMu.Unlock()
	if !old.readMu.TryLock() {
		return Result{}, errors.New("busy: read in progress; reconnect refused")
	}
	defer old.readMu.Unlock()
	if !old.writeMu.TryLock() {
		return Result{}, errors.New("busy: write in progress; reconnect refused")
	}
	defer old.writeMu.Unlock()
	m.mu.Lock()
	old.mu.Lock()
	if m.sessions[id] != old || old.disposed {
		old.mu.Unlock()
		m.mu.Unlock()
		return Result{}, errors.New("session disposed; use sess_connect")
	}
	if old.active != 0 || old.runningLocked() || old.reconnecting {
		old.mu.Unlock()
		m.mu.Unlock()
		return Result{}, errors.New("busy: session operation or exec in progress; reconnect refused")
	}
	o, cfg := old.options, old.config
	previousPeer := old.peer
	old.reconnecting = true
	old.active++ // Prevent idle collection while establishing the replacement.
	generation := m.generation
	m.pending[id] = cancel
	old.mu.Unlock()
	m.mu.Unlock()
	old.disconnect("")
	old.mu.Lock()
	old.buf = nil
	old.base, old.cursor = old.total, old.total
	old.discardTasksLocked()
	old.mu.Unlock()
	next := &Session{events: old.events, epoch: old.epoch + 1, id: id, name: old.name, peer: previousPeer, protocol: old.protocol, mode: old.mode, options: o, config: cfg,
		last: time.Now(), lastUse: time.Now(), connected: true, notify: make(chan struct{}, 1), done: make(chan struct{})}
	next.record("connecting", nil, "", "", nil)
	dialCtx, stop := context.WithTimeout(ctx, cfg.Duration(cfg.ConnectTimeoutMS))
	if o.Protocol == "ssh" {
		err = next.connectSSH(dialCtx, o)
	} else {
		err = next.connectTelnet(dialCtx, o)
	}
	if err == nil {
		err = dialCtx.Err()
	}
	stop()
	if err != nil {
		next.disconnectError(err, "reconnect failed")
	}
	// Publish only if close/close_all has not invalidated this handle in the meantime.
	m.mu.Lock()
	delete(m.pending, id)
	valid := generation == m.generation && m.sessions[id] == old
	old.mu.Lock()
	old.disposed = true
	old.options = Options{}
	old.buf = nil
	old.mu.Unlock()
	connectionWarning := ""
	if valid {
		next.mu.Lock()
		next.lastUse = time.Now()
		next.mu.Unlock()
		m.sessions[id] = next
		if err == nil {
			connectionWarning = m.recordConnectionLocked(next, o)
		}
	}
	m.mu.Unlock()
	if !valid {
		next.mu.Lock()
		next.disposed = true
		next.options = Options{}
		next.buf = nil
		next.mu.Unlock()
		next.disconnect("")
		return Result{}, errors.New("reconnect cancelled by close; use sess_connect")
	}
	next.record("session", nil, "", "", nil)
	go m.watch(next)
	v := next.status()
	v.Warning = connectionWarning
	next.mu.Lock()
	v.Peer = next.peer
	v.InstanceID = m.instanceID
	if err != nil && next.peer == previousPeer {
		v.Peer = nil
	}
	next.mu.Unlock()
	if err != nil {
		v.Reason = "aborted"
		if errors.Is(err, context.Canceled) {
			v.Reason = "cancelled"
		}
	}
	return v, nil
}
