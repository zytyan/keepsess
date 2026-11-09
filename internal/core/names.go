package core

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"time"
)

var processInstanceID = func() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("cannot generate process instance ID")
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}()

func newInstanceID() string           { return processInstanceID }
func (m *Manager) InstanceID() string { return m.instanceID }

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var reservedName = regexp.MustCompile(`^(s|task)[0-9]+$`)

func validateName(name string) error {
	if name != "" && (!namePattern.MatchString(name) || reservedName.MatchString(name)) {
		return errors.New("name must be 1..64 ASCII letters/digits/._-, start with a letter/digit, and not use reserved sN/taskN IDs; nothing sent")
	}
	return nil
}

// Manager.mu is held. Expired aliases are removed before name reuse.
func (m *Manager) resolveSessionLocked(ref string) string {
	for name, id := range m.sessionNames {
		if s := m.sessions[id]; s != nil {
			s.mu.Lock()
			disposed := s.disposed
			s.mu.Unlock()
			if disposed {
				delete(m.sessionNames, name)
			}
		} else if m.pending[id] == nil {
			delete(m.sessionNames, name)
		}
	}
	if id := m.sessionNames[ref]; id != "" {
		return id
	}
	return ref
}

func (m *Manager) RenameSession(ref, name string) (Result, error) {
	if name == "" {
		return Result{}, errors.New("name is required")
	}
	if err := validateName(name); err != nil {
		return Result{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.resolveSessionLocked(ref)
	s := m.sessions[id]
	if s == nil {
		return Result{}, m.sessionErrorLocked(ref)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disposed {
		return Result{}, m.sessionErrorLocked(ref)
	}
	if s.reconnecting {
		return Result{}, errors.New("busy: reconnect in progress")
	}
	if other := m.sessionNames[name]; other != "" && other != id {
		return Result{}, errors.New("duplicate session name")
	}
	delete(m.sessionNames, s.name)
	s.name = name
	s.recordLocked("session", nil, "", "", nil)
	m.sessionNames[name] = id
	s.lastUse = time.Now()
	return s.statusLocked(), nil
}

func (m *Manager) RenameTask(ref, name string) (Result, error) {
	if name == "" {
		return Result{}, errors.New("name is required")
	}
	if err := validateName(name); err != nil {
		return Result{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected *Task
	for _, s := range m.sessions {
		s.mu.Lock()
		s.pruneTasksLocked(time.Now())
		for _, t := range s.tasks {
			t.out.mu.Lock()
			matches := t.out.handle == ref || t.out.name == ref
			duplicate := t.out.name == name && !matches
			t.out.mu.Unlock()
			if duplicate {
				s.mu.Unlock()
				return Result{}, errors.New("duplicate task name")
			}
			if matches && !s.disposed && !s.reconnecting {
				selected = t
			}
		}
		s.mu.Unlock()
	}
	if selected == nil {
		return Result{}, &TaskLookupError{Handle: ref, InstanceID: m.instanceID}
	}
	s := selected.session
	s.mu.Lock()
	defer s.mu.Unlock()
	selected.out.mu.Lock()
	defer selected.out.mu.Unlock()
	if s.tasks[selected.out.handle] != selected || s.disposed || s.reconnecting || selected.out.disposed {
		return Result{}, errors.New("unknown or expired task; inspect sess_task_list")
	}
	selected.out.name = name
	selected.out.recordLocked("task", nil, "", "", nil)
	s.lastUse = time.Now()
	return selected.out.statusLocked(), nil
}
