package core

import (
	"strings"
	"time"
)

// Event is an independent observation, not a read receipt or proof of remote
// execution. Only explicitly whitelisted public metadata may enter this type.
type Event struct {
	Instance string    `json:"instance"`
	Seq      uint64    `json:"seq"`
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Session  string    `json:"session"`
	Handle   string    `json:"handle,omitempty"`
	Epoch    uint64    `json:"epoch"`
	Name     string    `json:"name,omitempty"`
	Protocol string    `json:"protocol,omitempty"`
	Mode     string    `json:"mode,omitempty"`
	Encoding string    `json:"encoding,omitempty"`
	ReadOnly bool      `json:"read_only"`
	Disposed bool      `json:"disposed"`
	Data     []byte    `json:"data,omitempty"`
	Command  string    `json:"command,omitempty"`
	Cwd      string    `json:"cwd,omitempty"`
	Status   Result    `json:"status"`
	Receipt  *Result   `json:"receipt,omitempty"`
}

// Emit must copy ownership and enqueue without blocking or calling core. Set
// the sink before Connect/Serve; it is immutable for the lifetime of a manager.
type EventSink interface{ Emit(Event) }

func (m *Manager) SetEventSink(sink EventSink) { m.events = sink }

func (s *Session) record(kind string, data []byte, command, cwd string, receipt *Result) {
	if s.events == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordLocked(kind, data, command, cwd, receipt)
}
func (s *Session) recordLocked(kind string, data []byte, command, cwd string, receipt *Result) {
	if s.events == nil {
		return
	}
	e := Event{Time: time.Now().UTC(), Kind: kind, Session: s.id, Handle: s.handle,
		Epoch: s.epoch, Name: s.name, Protocol: s.protocol, Mode: s.mode,
		Encoding: s.config.Encoding, ReadOnly: s.config.ReadOnly, Disposed: s.disposed,
		Command: command, Cwd: cwd, Status: eventResult(s.statusLocked())}
	if kind == "connecting" {
		e.Status.Connected = false
	}
	if receipt != nil {
		// No output duplication or unbounded retained error payloads.
		r := eventResult(*receipt)
		e.Receipt = &r
	}
	s.emitEvent(e, data)
}

func (s *Session) recordInput(data []byte, encoding string, receipt *Result) {
	if s.events == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := eventResult(*receipt)
	e := Event{Time: time.Now().UTC(), Kind: "input", Session: s.id, Handle: s.handle, Epoch: s.epoch, Encoding: encoding, Status: eventResult(s.statusLocked()), Receipt: &r}
	s.emitEvent(e, data)
}

func (s *Session) emitEvent(e Event, data []byte) {
	for {
		n := len(data)
		if n > 64<<10 {
			n = 64 << 10
		}
		e.Data = append([]byte(nil), data[:n]...)
		s.events.Emit(e)
		data = data[n:]
		if len(data) == 0 {
			break
		}
	}
}

// x/crypto includes the full wire command in an exec-request rejection. That
// wrapper may contain structured env values which are excluded from history.
// Raw terminal output and the original user command remain unchanged.
func eventResult(r Result) Result {
	r.Output = ""
	if strings.HasPrefix(r.Error, "ssh: command ") && strings.HasSuffix(r.Error, " failed") {
		r.Error = "SSH exec request rejected (wire command omitted from history)"
	}
	return r
}
