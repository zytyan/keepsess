package core

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"time"
)

// A bounded reason-only tombstone, never a credential or connection cache.
const retiredLimit = 128
const retiredRetention = 2 * time.Hour

type retiredSession struct {
	reason string
	at     time.Time
}
type SessionLookupError struct{ Session, Reason, NextAction string }

func (e *SessionLookupError) Error() string {
	detail := "parameters no longer retained"
	if e.Reason == "unknown_handle" {
		detail = "not retained in this process; may have been closed, expired, or belonged to an earlier service process"
	}
	return fmt.Sprintf("session %s unavailable: %s; use sess_connect (%s); do not replay prior input", e.Session, e.Reason, detail)
}

// Both helpers require Manager.mu.
func (m *Manager) rememberLocked(id, reason string) {
	if reason == "" {
		reason = "session_released"
	}
	if m.retired == nil {
		m.retired = make(map[string]retiredSession)
	}
	now := time.Now()
	for key, r := range m.retired {
		if now.Sub(r.at) >= retiredRetention {
			delete(m.retired, key)
		}
	}
	if _, ok := m.retired[id]; !ok && len(m.retired) >= retiredLimit {
		oldest := ""
		var at time.Time
		for key, r := range m.retired {
			if oldest == "" || r.at.Before(at) {
				oldest, at = key, r.at
			}
		}
		delete(m.retired, oldest)
	}
	m.retired[id] = retiredSession{reason, now}
}
func (m *Manager) sessionErrorLocked(id string) error {
	reason := "unknown_handle"
	if r, ok := m.retired[id]; ok {
		if time.Since(r.at) < retiredRetention {
			reason = r.reason
		} else {
			delete(m.retired, id)
		}
	}
	return &SessionLookupError{Session: id, Reason: reason, NextAction: "sess_connect"}
}
func classifyDisconnect(err error, detail string) string {
	if errors.Is(err, syscall.ECONNRESET) {
		return "reset_by_peer"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "transport_timeout"
	}
	if errors.Is(err, io.EOF) || (err == nil && detail == "") {
		return "remote_eof"
	}
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "connection reset by peer"), strings.Contains(lower, "forcibly closed"):
		return "reset_by_peer"
	case strings.Contains(lower, "ssh: disconnect, reason"):
		return "ssh_disconnect"
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline exceeded"):
		return "transport_timeout"
	default:
		return "transport_error"
	}
}
func (s *Session) disconnectError(err error, prefix string) {
	if err == nil {
		s.disconnectWith("remote_eof", "")
		return
	}
	s.disconnectWith(classifyDisconnect(err, err.Error()), prefix+": "+err.Error())
}

// Only one global SSH request is outstanding. A negative reply is still a reply.
// Timeout closes the transport so the pending request and all tasks unblock.
func (s *Session) sshKeepalive(send func() error) {
	for {
		s.mu.Lock()
		interval, timeout := s.config.SSHKeepaliveMS, s.config.SSHKeepaliveTimeoutMS
		wake := s.keepaliveWake
		s.mu.Unlock()
		var timer *time.Timer
		var tick <-chan time.Time
		if interval > 0 {
			timer = time.NewTimer(time.Duration(interval) * time.Millisecond)
			tick = timer.C
		}
		select {
		case <-s.done:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-wake:
			if timer != nil {
				timer.Stop()
			}
			continue
		case <-tick:
		}
		reply := make(chan error, 1)
		go func() { reply <- send() }()
		deadline := time.NewTimer(time.Duration(timeout) * time.Millisecond)
		select {
		case <-s.done:
			deadline.Stop()
			return
		case err := <-reply:
			deadline.Stop()
			if err != nil {
				s.disconnectError(err, "SSH keepalive failed")
				return
			}
		case <-deadline.C:
			s.disconnectWith("keepalive_timeout", "SSH keepalive response timeout; transport closed; unfinished task outcomes unknown; use sess_reconnect")
			return
		}
	}
}
