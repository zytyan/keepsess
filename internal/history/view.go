package history

import (
	"errors"
	"os"

	"github.com/zytyan/keepsess/internal/core"
)

// View reads another process's journal without opening a writer or touching its
// projections. A live view receives observations with the original sequence.
func View(root, instance string) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return &Store{root: root, instance: instance, view: true, done: make(chan struct{}), pending: make(map[uint64]core.Event), subscribers: make(map[*Subscription]bool)}, nil
}
func (s *Store) Update(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.view || s.closed {
		return
	}
	s.state = state
	for seq, e := range s.pending {
		if seq <= state.WrittenSeq {
			s.queuedBytes -= eventSize(e)
			delete(s.pending, seq)
		}
	}
}
func (s *Store) Observe(e core.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.view || s.closed {
		return errors.New("history view closed")
	}
	if e.Instance != s.instance || e.Seq == 0 {
		return errors.New("invalid observation identity")
	}
	size := eventSize(e)
	if e.Seq > s.state.WrittenSeq {
		if old, ok := s.pending[e.Seq]; ok {
			s.queuedBytes -= eventSize(old)
		}
		if s.queuedBytes+size > queueBytes || len(s.pending) >= 256 {
			return errors.New("live history view overflow; reconnect to disk history")
		}
		s.pending[e.Seq] = e
		s.queuedBytes += size
	}
	if e.Seq > s.state.LastSeq {
		s.state.LastSeq = e.Seq
	}
	for sub := range s.subscribers {
		if sub.bytes+size > queueBytes {
			close(sub.C)
			delete(s.subscribers, sub)
			continue
		}
		select {
		case sub.C <- e:
			sub.bytes += size
		default:
			close(sub.C)
			delete(s.subscribers, sub)
		}
	}
	return nil
}
