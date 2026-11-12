package history

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/core"
)

func savedPassword(s string) *string { return &s }

func connectionFixture(instance, session, protocol string) core.ConnectionRecord {
	now := time.Now().UTC()
	return core.ConnectionRecord{Version: 1, Instance: instance, Session: session, Protocol: protocol, Mode: "stream", FirstConnected: now, LastConnected: now,
		Target:   core.ConnectionEndpoint{Host: "localhost", Port: 23, Username: "user", Password: "target-secret"},
		SSHJumps: []core.ConnectionEndpoint{{Host: "jump", Port: 22, Username: "jumpuser", Password: "jump-secret"}}}
}
func TestConnectionPersistenceRedactionManualAndPagination(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "old", 0)
	if err != nil {
		t.Fatal(err)
	}
	first := connectionFixture("old", "s1", "telnet")
	for _, r := range []core.ConnectionRecord{first, connectionFixture("old", "s2", "ssh")} {
		if err = s.RecordConnection(r); err != nil {
			t.Fatal(err)
		}
	}
	s.Emit(core.Event{Kind: "session", Session: "s1", Name: "renamed", Protocol: "telnet", Status: core.Result{Connected: true}})
	waitWritten(t, s, s.Status().LastSeq)
	if err = s.SaveConnectionCredentials("old", "s1", "manual-user", savedPassword("manual-secret")); err != nil {
		t.Fatal(err)
	}
	// The agent's successful reconnect updates its own file, not the manual overlay.
	next := first
	next.LastConnected = next.LastConnected.Add(time.Minute)
	next.FirstConnected = next.LastConnected
	if err = s.RecordConnection(next); err != nil {
		t.Fatal(err)
	}
	s.Close()
	v, err := View(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	page, err := v.Connections(context.Background(), "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor != "old/s1" {
		t.Fatal(page, err)
	}
	r := page.Items[0]
	if !r.Manual || r.Target.Username != "manual-user" || !r.Target.HasPassword || r.Target.Password != "" || r.SSHJumps[0].Password != "" || r.Name != "renamed" || !r.FirstConnected.Equal(first.FirstConnected) || !r.LastConnected.Equal(next.LastConnected) {
		t.Fatal("incorrect redacted metadata")
	}
	b, _ := json.Marshal(page)
	if strings.Contains(string(b), "secret") {
		t.Fatal("password in list")
	}
	page, err = v.Connections(context.Background(), page.NextCursor, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Session != "s2" || page.NextCursor != "" {
		t.Fatal(page, err)
	}
	r, err = v.Connection("old", "s1", true)
	if err != nil || r.Target.Password != "manual-secret" || r.SSHJumps[0].Password != "jump-secret" {
		t.Fatal("password not retained", err)
	}
	if err = v.SaveConnectionCredentials("old", "s1", "", savedPassword("")); err != nil {
		t.Fatal(err)
	}
	r, err = v.Connection("old", "s1", false)
	if err != nil || r.Target.HasPassword || r.Target.Username != "" {
		t.Fatal("clear failed", err)
	}
	if err = v.SaveConnectionCredentials("old", "s2", "u", savedPassword("p")); err == nil {
		t.Fatal("SSH manual edit allowed")
	}
	if _, err = v.Connection("old", "s99", true); err == nil {
		t.Fatal("missing record not reported")
	}
	for _, p := range []string{"connections", "connections/s1.json", "connections/s1.credentials.json", "connections/s2.json"} {
		st, e := os.Stat(filepath.Join(root, "old", p))
		if e != nil {
			t.Fatal(e)
		}
		if runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
			t.Fatal("non-private connection storage", p)
		}
	}
	// Private files are outside ordinary replay/history and survive empty cleanup.
	if cleanupEmpty(filepath.Join(root, "old")) {
		t.Fatal("private records deleted by empty cleanup")
	}
}

func TestConnectionWriteFailureAndBoundedQueue(t *testing.T) {
	s, err := Open(t.TempDir(), "failure", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordConnection(connectionFixture("failure", "s1", "ssh")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s.Status().ConnectionError == "" || s.Status().LostEvents != 0 {
		t.Fatal("storage failure not reported independently")
	}
	page, err := s.Connections(context.Background(), "", 50)
	if err != nil || len(page.Items) != 0 || len(page.Warnings) != 1 {
		t.Fatal(page, err)
	}
	// A full queue rejects promptly and contains no public event carrying credentials.
	full := &Store{instance: "full", queue: make(chan queued, 1)}
	full.queue <- queued{}
	if err = full.RecordConnection(connectionFixture("full", "s1", "ssh")); err == nil || full.Status().ConnectionError == "" {
		t.Fatal("full queue not reported")
	}
}

func TestConnectionConcurrentManualSavesAndValidation(t *testing.T) {
	s, err := Open(t.TempDir(), "parallel", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := connectionFixture("parallel", "s1", "telnet")
	if err = s.RecordConnection(r); err != nil {
		t.Fatal(err)
	}
	s.Emit(core.Event{Kind: "session", Session: "s1"})
	waitWritten(t, s, s.Status().LastSeq)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.SaveConnectionCredentials("parallel", "s1", "same", savedPassword("same")); e != nil {
				t.Error(e)
			}
			if _, e := s.Connection("parallel", "s1", true); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if _, err = s.Connection("../outside", "s1", true); err == nil {
		t.Fatal("instance traversal allowed")
	}
	if _, err = s.Connection("parallel", "../s1", true); err == nil {
		t.Fatal("session traversal allowed")
	}
	if _, err = s.Connections(context.Background(), "invalid", 50); err == nil {
		t.Fatal("invalid cursor allowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Connections(ctx, "", 50); err == nil {
		t.Fatal("cancel ignored")
	}
	if err = s.SaveConnectionCredentials("parallel", "s1", "user", savedPassword(strings.Repeat("p", 65537))); err == nil {
		t.Fatal("unbounded credentials")
	}
	if runtime.GOOS != "windows" {
		dir, _ := s.connectionDir("parallel", false)
		if err = os.Symlink(filepath.Join(dir, "s1.json"), filepath.Join(dir, "s2.json")); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Connection("parallel", "s2", true); err == nil {
			t.Fatal("symlink followed")
		}
	}
}

func TestConnectionFaultsRetainWarningsAndOtherRecords(t *testing.T) {
	root := t.TempDir()
	bad, err := Open(root, "bad", 0)
	if err != nil {
		t.Fatal(err)
	}
	// A filesystem obstruction fails persistence without losing ordinary history.
	if err = os.WriteFile(filepath.Join(root, "bad", "connections"), []byte("obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = bad.RecordConnection(connectionFixture("bad", "s1", "ssh")); err != nil {
		t.Fatal(err)
	}
	bad.Emit(core.Event{Kind: "session", Session: "s1"})
	bad.Close()
	if bad.Status().ConnectionError == "" || bad.Status().LostEvents != 0 {
		t.Fatal("filesystem fault not reported independently")
	}
	good, err := Open(root, "good", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = good.RecordConnection(connectionFixture("good", "s1", "ssh")); err != nil {
		t.Fatal(err)
	}
	good.Close()
	page, err := good.Connections(context.Background(), "", 50)
	if err != nil || len(page.Items) != 1 || len(page.Warnings) == 0 {
		t.Fatal("storage fault hid healthy records", err)
	}
	huge, err := Open(root, "oversize", 0)
	if err != nil {
		t.Fatal(err)
	}
	r := connectionFixture("oversize", "s1", "ssh")
	r.Target.Password = strings.Repeat("p", connectionLimit)
	if err = huge.RecordConnection(r); err == nil {
		t.Fatal("oversized connection queued")
	}
	huge.Close()
	if _, err = os.Stat(filepath.Join(root, "oversize", "state.json")); err != nil {
		t.Fatal("failed recording state deleted", err)
	}
	if cleanupEmpty(filepath.Join(root, "oversize")) {
		t.Fatal("cleanup deleted persistence failure warning")
	}
}
