// Package history persists a bounded, asynchronous observation stream. It never
// owns terminal cursors and never blocks a remote reader on disk or a browser.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zytyan/keepsess/internal/core"
)

const segmentBytes = 4 << 20
const queueBytes = 8 << 20

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type State struct {
	Recording  bool   `json:"recording"`
	Error      string `json:"error,omitempty"`
	LastSeq    uint64 `json:"last_seq"`
	WrittenSeq uint64 `json:"written_seq"`
	DurableSeq uint64 `json:"durable_seq"`
	LostEvents uint64 `json:"lost_events"`
}
type Instance struct {
	ID          string    `json:"id"`
	Name        string    `json:"name,omitempty"`
	HumanName   string    `json:"human_name,omitempty"`
	Started     time.Time `json:"started"`
	Current     bool      `json:"current"`
	PID         int       `json:"pid,omitempty"`
	HasSessions *bool     `json:"has_sessions,omitempty"`
}
type Page struct {
	Events []core.Event `json:"events"`
	Next   uint64       `json:"next"`
	More   bool         `json:"more"`
	State  State        `json:"history"`
}
type Record struct {
	Event   core.Event `json:"event"`
	Command string     `json:"command,omitempty"`
	Cwd     string     `json:"cwd,omitempty"`
}
type queued struct {
	event core.Event
	size  int
}
type Store struct {
	readCursorMu   sync.Mutex
	readCursors    map[readCursorKey]readCursor
	readCursorKeys []readCursorKey
	readCursorNext int
	root, instance string
	view           bool
	maxBytes       int64
	mu             sync.Mutex
	state          State
	queuedBytes    int
	queue          chan queued
	done           chan struct{}
	closed         bool
	hasSessions    bool
	owner          *os.File
	once           sync.Once
	pending        map[uint64]core.Event
	subscribers    map[*Subscription]bool
	recoveryMu     sync.Mutex
	recoveries     map[string]*recovery
	// File and projection mutations belong exclusively to the writer goroutine.
	file              *os.File
	segmentSize, used int64
	metaHasSessions   bool
}

func Open(root, instance string, maxBytes int64) (*Store, error) {
	if !validID.MatchString(instance) || maxBytes < 0 {
		return nil, errors.New("invalid history instance or capacity")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	dir := filepath.Join(root, instance)
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, fmt.Errorf("create private instance history: %w", err)
	}
	owner, err := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockOwner(owner); err != nil {
		owner.Close()
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			owner.Close()
		}
	}()
	s := &Store{root: root, instance: instance, owner: owner, maxBytes: maxBytes, queue: make(chan queued, 256), done: make(chan struct{}), state: State{Recording: true}, pending: make(map[uint64]core.Event), subscribers: make(map[*Subscription]bool)}
	if err := writeJSON(filepath.Join(dir, "instance.json"), Instance{ID: instance, Started: time.Now().UTC(), HasSessions: new(bool)}); err != nil {
		return nil, err
	}
	// Capacity counts all existing archives, not just the current process.
	if maxBytes > 0 {
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				st, e := d.Info()
				if e != nil {
					return e
				}
				s.used += st.Size()
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	started = true
	go s.run()
	return s, nil
}
func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	// Both files live on the same filesystem; replace the projection atomically.
	if err = os.Rename(path+".tmp", path); err != nil {
		return err
	}
	return nil
}
func (s *Store) Emit(e core.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if e.Session != "" {
		s.hasSessions = true
	}
	s.state.LastSeq++
	e.Seq = s.state.LastSeq
	e.Instance = s.instance
	size := eventSize(e)
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
	if s.state.Error != "" {
		s.state.LostEvents++
		return
	}
	if s.queuedBytes+size > queueBytes {
		s.failLocked("history queue overflow; recording stopped, later output is not archived")
		s.state.LostEvents++
		return
	}
	select {
	case s.queue <- queued{e, size}:
		s.queuedBytes += size
		s.pending[e.Seq] = e
	default:
		s.failLocked("history queue full; recording stopped, later output is not archived")
		s.state.LostEvents++
	}
}
func (s *Store) failLocked(why string) {
	if s.state.Error == "" {
		s.state.Error = why
	}
	s.state.Recording = false
}
func (s *Store) Status() State { s.mu.Lock(); defer s.mu.Unlock(); return s.state }
func (s *Store) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.view {
			close(s.done)
			for sub := range s.subscribers {
				close(sub.C)
				delete(s.subscribers, sub)
			}
			s.mu.Unlock()
			return
		}
		s.state.Recording = false
		close(s.queue)
		for sub := range s.subscribers {
			close(sub.C)
			delete(s.subscribers, sub)
		}
		s.mu.Unlock()
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			s.mu.Lock()
			s.failLocked("history shutdown drain timeout; archive may be incomplete")
			s.mu.Unlock()
		}
	})
}
func (s *Store) run() {
	defer close(s.done)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var last uint64
	writeFailed := false
	syncDisk := func() {
		if s.file != nil {
			if err := s.file.Sync(); err != nil {
				s.mu.Lock()
				s.failLocked("history sync: " + err.Error())
				s.mu.Unlock()
			} else {
				s.mu.Lock()
				s.state.DurableSeq = last
				s.mu.Unlock()
			}
		}
		if err := writeJSON(filepath.Join(s.root, s.instance, "state.json"), s.Status()); err != nil {
			s.mu.Lock()
			s.failLocked("history status write: " + err.Error())
			s.mu.Unlock()
		}
	}
	defer func() {
		syncDisk()
		if s.file != nil {
			_ = s.file.Close()
		}
		_ = s.owner.Close()
		s.mu.Lock()
		empty := !s.hasSessions && s.state.Error == "" && s.state.LostEvents == 0
		s.mu.Unlock()
		if empty {
			if err := os.RemoveAll(filepath.Join(s.root, s.instance)); err != nil {
				s.mu.Lock()
				s.failLocked("empty history cleanup: " + err.Error())
				s.mu.Unlock()
			}
		}
	}()
	for {
		select {
		case <-tick.C:
			syncDisk()
		case q, ok := <-s.queue:
			if !ok {
				return
			}
			s.mu.Lock()
			s.queuedBytes -= q.size
			s.mu.Unlock()
			if writeFailed {
				s.mu.Lock()
				delete(s.pending, q.event.Seq)
				s.state.LostEvents++
				s.mu.Unlock()
				continue
			}
			if err := s.append(q.event); err != nil {
				writeFailed = true
				s.mu.Lock()
				s.failLocked("history write: " + err.Error())
				s.state.LostEvents++
				delete(s.pending, q.event.Seq)
				s.mu.Unlock()
				continue
			}
			last = q.event.Seq
			s.mu.Lock()
			s.state.WrittenSeq = last
			delete(s.pending, last)
			s.mu.Unlock()
		}
	}
}
func (s *Store) append(e core.Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if s.maxBytes > 0 && s.used+int64(len(b)) > s.maxBytes {
		return errors.New("capacity reached; recording stopped")
	}
	dir := filepath.Join(s.root, s.instance)
	if s.file == nil || s.segmentSize+int64(len(b)) > segmentBytes {
		if s.file != nil {
			if err = s.file.Sync(); err != nil {
				return err
			}
			if err = s.file.Close(); err != nil {
				return err
			}
		}
		s.file, err = os.OpenFile(filepath.Join(dir, fmt.Sprintf("%020d.jsonl", e.Seq)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		s.segmentSize = 0
	}
	n, err := s.file.Write(b)
	s.segmentSize += int64(n)
	s.used += int64(n)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	if e.Kind == "instance" || (e.Session != "" && !s.metaHasSessions) {
		path := filepath.Join(dir, "instance.json")
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var meta Instance
		if err = json.Unmarshal(data, &meta); err != nil {
			return err
		}
		changed := false
		if e.Kind == "instance" {
			meta.Name = e.Name
			changed = true
		}
		if e.Session != "" && (meta.HasSessions == nil || !*meta.HasSessions) {
			present := true
			meta.HasSessions = &present
			changed = true
		}
		if changed {
			if err = writeJSON(path, meta); err != nil {
				return err
			}
		}
		if e.Session != "" {
			s.metaHasSessions = true
		}
	}

	if e.Kind == "instance" {
		return nil
	}
	if e.Kind == "output" || e.Kind == "input" || e.Kind == "receipt" {
		return nil
	}
	return project(dir, e)
}

func project(dir string, e core.Event) error {
	r := Record{Event: e, Command: e.Command, Cwd: e.Cwd}
	r.Event.EverConnected = e.EverConnected || (e.Kind != "connecting" && e.Status.Connected)
	r.Event.Data = nil
	key := e.Session
	if e.Handle != "" {
		key += "_" + e.Handle
	}
	if !validID.MatchString(key) {
		return errors.New("invalid journal identity")
	}
	path := filepath.Join(dir, key+".meta")
	if old, err := os.ReadFile(path); err == nil {
		var prior Record
		if json.Unmarshal(old, &prior) == nil {
			r.Event.EverConnected = r.Event.EverConnected || prior.Event.EverConnected || (prior.Event.Kind != "connecting" && prior.Event.Status.Connected)
			if r.Command == "" {
				r.Command = prior.Command
				r.Cwd = prior.Cwd
			}
		}
	}
	return writeJSON(path, r)
}
func (s *Store) Instances() ([]Instance, error) {
	ds, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	out := []Instance{}
	for _, d := range ds {
		if !d.IsDir() || !validID.MatchString(d.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.root, d.Name(), "instance.json"))
		if err != nil {
			continue
		}
		var i Instance
		if json.Unmarshal(b, &i) != nil || i.ID != d.Name() {
			continue
		}
		dir := filepath.Join(s.root, d.Name())
		if i.HasSessions != nil && !*i.HasSessions && ownerActive(dir) {
			continue
		}
		if cleanupEmpty(dir) {
			continue
		}
		has, known := sessionPresence(dir, i)
		if i.ID == s.instance {
			s.mu.Lock()
			has = has || s.hasSessions
			s.mu.Unlock()
		}
		if known && !has {
			continue
		}
		labelBytes, labelErr := os.ReadFile(filepath.Join(dir, "human-name.json"))
		if labelErr != nil && !errors.Is(labelErr, os.ErrNotExist) {
			return nil, labelErr
		}
		var label humanLabel
		if labelErr == nil {
			if err := json.Unmarshal(labelBytes, &label); err != nil {
				return nil, err
			}
		}
		i.HumanName = label.Name
		i.Current = i.ID == s.instance
		out = append(out, i)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Current != out[j].Current {
			return out[i].Current
		}
		return out[i].Started.After(out[j].Started)
	})
	return out, nil
}
func (s *Store) instanceDir(instance string) (string, error) {
	if !validID.MatchString(instance) {
		return "", errors.New("invalid instance")
	}
	return filepath.Join(s.root, instance), nil
}

// Catalog reads projections rebuilt from the journal if necessary. Pagination
// bounds each response; output bytes never accumulate in a catalog.
func (s *Store) Catalog(instance string, offset, limit int) ([]Record, bool, error) {
	dir, err := s.instanceDir(instance)
	if err != nil {
		return nil, false, err
	}
	if instance != s.instance {
		if err = s.Rebuild(instance); err != nil {
			return nil, false, err
		}
		dir = s.projectionDir(instance)
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	out := []Record{}
	seen := 0
	for {
		ds, readErr := f.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return nil, false, readErr
		}
		for _, d := range ds {
			if !strings.HasSuffix(d.Name(), ".meta") {
				continue
			}
			if seen < offset {
				seen++
				continue
			}
			if len(out) >= limit {
				return out, true, nil
			}
			b, err := os.ReadFile(filepath.Join(dir, d.Name()))
			if err != nil {
				return nil, false, err
			}
			var r Record
			if err = json.Unmarshal(b, &r); err != nil {
				return nil, false, err
			}
			if instance != s.instance {
				r.Event.Status.Connected = false
				if r.Event.Status.Running || r.Event.Status.State == "starting" {
					r.Event.Status.State = "unknown"
					r.Event.Status.Running = false
				}
				r.Event.Disposed = true
			}
			out = append(out, r)
		}
		if readErr == io.EOF {
			break
		}
	}
	return out, false, nil
}

// Read uses segment-start sequence numbers as a sparse index. At most one
// segment (4 MiB) is scanned per request; Next also advances across filtered
// records so scans and replay are always bounded.
func (s *Store) Read(instance string, after uint64, limit int) (Page, error) {
	p := Page{Events: []core.Event{}, Next: after}
	dir, err := s.instanceDir(instance)
	if err != nil {
		return p, err
	}
	ds, err := os.ReadDir(dir)
	if err != nil {
		return p, err
	}
	names := []string{}
	for _, d := range ds {
		if strings.HasSuffix(d.Name(), ".jsonl") {
			names = append(names, d.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		p.State = s.archiveState(instance)
		return p, nil
	}
	idx := 0
	for i, n := range names {
		seq, _ := strconv.ParseUint(strings.TrimSuffix(n, ".jsonl"), 10, 64)
		if seq <= after+1 {
			idx = i
		} else {
			break
		}
	}
	f, err := os.Open(filepath.Join(dir, names[idx]))
	if err != nil {
		return p, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return p, err
	}
	position := s.readPosition(readCursorKey{instance: instance, segment: names[idx], after: after}, info)
	if position > 0 {
		if _, err = f.Seek(position, io.SeekStart); err != nil {
			return p, err
		}
	}
	scan := bufio.NewScanner(io.LimitReader(f, segmentBytes+(3<<20)))
	scan.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := bufio.ScanLines(data, atEOF)
		position += int64(advance)
		return advance, token, err
	})
	scan.Buffer(make([]byte, 64<<10), 3<<20)
	count := 0
	bytesRead := 0
	for scan.Scan() {
		b := scan.Bytes()
		if len(b) == 0 {
			continue
		}
		var e core.Event
		if err = json.Unmarshal(b, &e); err != nil {
			// Only a final, unterminated record may be a crash tail. Stop there and
			// expose the gap instead of accepting malformed history as complete.
			p.State = s.archiveState(instance)
			if instance == s.instance {
				p.More = false
				return p, nil
			}
			p.State.Error = "history contains an incomplete/corrupt record"
			p.State.Recording = false
			return p, nil
		}
		if e.Seq <= after {
			continue
		}
		p.Events = append(p.Events, e)
		p.Next = e.Seq
		count++
		bytesRead += len(b)
		if count >= limit || bytesRead >= 512<<10 {
			p.More = true
			break
		}
	}
	if err = scan.Err(); err != nil {
		return p, err
	}
	if idx+1 < len(names) {
		p.More = true
	}
	p.State = s.archiveState(instance)
	s.rememberReadPosition(readCursorKey{instance: instance, segment: names[idx], after: p.Next}, position, info)
	return p, nil
}
func (s *Store) archiveState(instance string) State {
	if instance == s.instance {
		return s.Status()
	}
	var st State
	b, err := os.ReadFile(filepath.Join(s.root, instance, "state.json"))
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	st.Recording = false
	return st
}

// ErrIndexing asks callers to request the next bounded recovery batch.
var ErrIndexing = errors.New("archive index rebuilding")

type recovery struct {
	after uint64
	done  bool
}

func (s *Store) projectionDir(instance string) string {
	return filepath.Join(s.root, s.instance, "archive-"+instance)
}

// Rebuild writes a reader-private projection cache. It never modifies another
// process's files, even if that instance is still running. Each call scans a
// bounded batch and resumes on the next request; no output is held in memory.
func (s *Store) Rebuild(instance string) error {
	if !validID.MatchString(instance) || instance == s.instance {
		return errors.New("invalid archive instance")
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	if s.recoveries == nil {
		s.recoveries = make(map[string]*recovery)
	}
	r := s.recoveries[instance]
	if r == nil {
		if len(s.recoveries) >= 128 {
			for key, prior := range s.recoveries {
				if prior.done {
					delete(s.recoveries, key)
					break
				}
			}
			if len(s.recoveries) >= 128 {
				return errors.New("too many concurrent archive index builds; retry later")
			}
		}
		r = &recovery{}
		s.recoveries[instance] = r
	}
	if r.done {
		return nil
	}
	dir := s.projectionDir(instance)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for batch := 0; batch < 16; batch++ {
		select {
		case <-s.done:
			return errors.New("history closed")
		default:
		}
		p, err := s.Read(instance, r.after, 128)
		if err != nil {
			return err
		}
		for _, e := range p.Events {
			if e.Kind == "output" || e.Kind == "input" || e.Kind == "receipt" || e.Kind == "instance" {
				continue
			}
			if err = project(dir, e); err != nil {
				return err
			}
		}
		previous := r.after
		r.after = p.Next
		if !p.More || p.Next <= previous {
			r.done = true
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
	}
	return ErrIndexing
}

func eventSize(e core.Event) int { return len(e.Data) + len(e.Command) + 2048 }

type Subscription struct {
	C     chan core.Event
	store *Store
	bytes int
}

func (s *Store) Subscribe() (*Subscription, []core.Event, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.subscribers) >= 32 {
		return nil, nil, 0, errors.New("subscriber limit or history closed")
	}
	sub := &Subscription{C: make(chan core.Event, 128), store: s}
	s.subscribers[sub] = true
	pending := make([]core.Event, 0, len(s.pending))
	for _, e := range s.pending {
		pending = append(pending, e)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Seq < pending[j].Seq })
	return sub, pending, s.state.WrittenSeq, nil
}
func (sub *Subscription) Ack(e core.Event) {
	sub.store.mu.Lock()
	sub.bytes -= eventSize(e)
	sub.store.mu.Unlock()
}
func (sub *Subscription) Close() {
	s := sub.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscribers[sub] {
		delete(s.subscribers, sub)
		close(sub.C)
	}
}
