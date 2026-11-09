package history

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/core"
)

func waitWritten(t *testing.T, s *Store, seq uint64) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		if s.Status().WrittenSeq >= seq {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("history did not drain: %+v", s.Status())
}
func TestJournalPaginationAndIndependentSubscriptions(t *testing.T) {
	s, e := Open(t.TempDir(), "instance", 0)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	sub, _, _, e := s.Subscribe()
	if e != nil {
		t.Fatal(e)
	}
	defer sub.Close()
	for i := 0; i < 12; i++ {
		s.Emit(core.Event{Kind: "output", Session: "s1", Data: []byte{byte(i)}})
	}
	waitWritten(t, s, 12)
	p, e := s.Read("instance", 0, 5)
	if e != nil || len(p.Events) != 5 || p.Next != 5 || !p.More {
		t.Fatal(p, e)
	}
	p, e = s.Read("instance", p.Next, 20)
	if e != nil || len(p.Events) != 7 || p.Next != 12 {
		t.Fatal(p, e)
	}
	for i := 1; i <= 12; i++ {
		select {
		case e := <-sub.C:
			sub.Ack(e)
			if e.Seq != uint64(i) {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber missed output")
		}
	}
	if _, e = s.Read("../escape", 0, 10); e == nil {
		t.Fatal("accepted path traversal")
	}
}
func TestArchiveCloseRenameRecoveryAndPermissions(t *testing.T) {
	root := t.TempDir()
	s, e := Open(root, "old", 0)
	if e != nil {
		t.Fatal(e)
	}
	s.Emit(core.Event{Kind: "session", Session: "s1", Name: "prod", Status: core.Result{Connected: true}})
	s.Emit(core.Event{Kind: "command", Session: "s1", Handle: "task1", Command: "echo hi", Cwd: "/srv", Status: core.Result{Running: true, State: "running"}})
	s.Emit(core.Event{Kind: "session", Session: "s1", Name: "renamed", Disposed: true})
	s.Close()
	if runtime.GOOS != "windows" {
		ds, _ := os.ReadDir(filepath.Join(root, "old"))
		for _, d := range ds {
			st, _ := d.Info()
			if st.Mode().Perm() != 0600 {
				t.Fatal(d.Name(), st.Mode())
			}
		}
	}
	next, e := Open(root, "new", 0)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	_ = os.Remove(filepath.Join(root, "old", "s1.meta"))
	records, _, e := next.Catalog("old", 0, 100)
	if e != nil || len(records) != 2 {
		t.Fatal(records, e)
	}
	for _, r := range records {
		if !r.Event.Disposed || r.Event.Status.Connected {
			t.Fatal(r)
		}
		if r.Event.Handle != "" && (r.Command != "echo hi" || r.Event.Status.State != "unknown") {
			t.Fatal(r)
		}
	}
	// Projections can be reconstructed after a crash or missing index files.
	_ = os.Remove(filepath.Join(root, "old", "s1.meta"))
	if e = next.Rebuild("old"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(next.projectionDir("old"), "s1.meta"))
	if !bytes.Contains(b, []byte("renamed")) {
		t.Fatal(string(b))
	}
	fs, e := os.OpenFile(filepath.Join(root, "old", "00000000000000000001.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = fs.WriteString(`{"crash":`)
	_ = fs.Close()
	p, e := next.Read("old", 0, 100)
	if e != nil || p.Next != 3 || p.State.Error == "" {
		t.Fatal(p, e)
	}
}
func TestCapacityFaultDoesNotBlockLiveOutput(t *testing.T) {
	s, e := Open(t.TempDir(), "instance", 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	sub, _, _, _ := s.Subscribe()
	defer sub.Close()
	s.Emit(core.Event{Kind: "output", Session: "s1", Data: []byte("first")})
	until := time.Now().Add(time.Second)
	for s.Status().Error == "" && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if s.Status().Recording || !strings.Contains(s.Status().Error, "capacity") {
		t.Fatal(s.Status())
	}
	s.Emit(core.Event{Kind: "output", Session: "s1", Data: []byte("still live")})
	for i := 0; i < 2; i++ {
		select {
		case <-sub.C:
		case <-time.After(time.Second):
			t.Fatal("disk fault blocked live stream")
		}
	}
	if s.Status().LostEvents != 2 {
		t.Fatal(s.Status())
	}
}
func TestSlowSubscriberAndQueueAreBounded(t *testing.T) {
	s, e := Open(t.TempDir(), "instance", 0)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	sub, _, _, _ := s.Subscribe()
	for i := 0; i < 300; i++ {
		s.Emit(core.Event{Kind: "output", Session: "s1", Data: make([]byte, 65536)})
	}
	count := 0
	for range sub.C {
		count++
	}
	if count > 128 {
		t.Fatal(count)
	}
	if s.Status().LastSeq != 300 {
		t.Fatal(s.Status())
	}
}
func TestSegmentReplayHasNoGaps(t *testing.T) {
	s, e := Open(t.TempDir(), "instance", 0)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for i := uint64(1); i <= 64; i++ {
		s.Emit(core.Event{Kind: "output", Session: "s1", Data: make([]byte, 65536)})
		waitWritten(t, s, i)
	}
	var after uint64
	for i := 0; i < 100; i++ {
		p, e := s.Read("instance", after, 7)
		if e != nil {
			t.Fatal(e)
		}
		for _, event := range p.Events {
			if event.Seq != after+1 {
				t.Fatal("gap", after, event.Seq)
			}
			after = event.Seq
		}
		if !p.More {
			break
		}
	}
	if after != 64 {
		t.Fatal(after)
	}
	b, e := json.Marshal(s.Status())
	if e != nil || !bytes.Contains(b, []byte(`"last_seq":64`)) {
		t.Fatal(string(b), e)
	}
}

func TestShutdownWaitIsBoundedWhenFilesystemWriterStalls(t *testing.T) {
	s := &Store{queue: make(chan queued), done: make(chan struct{}), state: State{Recording: true}}
	start := time.Now()
	s.Close()
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatal("unbounded shutdown", elapsed)
	}
	if !strings.Contains(s.Status().Error, "shutdown drain timeout") {
		t.Fatal(s.Status())
	}
}
