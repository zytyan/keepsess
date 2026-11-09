package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zytyan/keepsess/internal/core"
)

func TestEmptyOwnerHelper(t *testing.T) {
	root := os.Getenv("KEEPSESS_EMPTY_OWNER_TEST")
	if root == "" {
		return
	}
	s, err := Open(root, "crashed", 0)
	if err != nil {
		t.Fatal(err)
	}
	s.Emit(core.Event{Kind: "instance", Name: "empty work"})
	waitWritten(t, s, 1)
	fmt.Println("ready")
	// The parent kills this process, deliberately skipping Close.
	select {}
}

func TestEmptyInstanceLifecycle(t *testing.T) {
	root := t.TempDir()
	reader, err := View(root, ".observer")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	s, err := Open(root, "empty", 0)
	if err != nil {
		t.Fatal(err)
	}
	s.Emit(core.Event{Kind: "instance", Name: "named but empty"})
	waitWritten(t, s, 1)
	list, err := reader.Instances()
	if err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	if _, err = os.Stat(filepath.Join(root, "empty")); err != nil {
		t.Fatal("live empty owner was deleted", err)
	}
	s.Close()
	if _, err = os.Stat(filepath.Join(root, "empty")); !os.IsNotExist(err) {
		t.Fatal("closed empty history retained", err)
	}
	// The graceful owner knows its entire lifecycle, even when a name-only
	// journal grows beyond the bounded crash-inspection page.
	many, err := Open(root, "many-names", 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 512; i++ {
		many.Emit(core.Event{Kind: "instance", Name: strings.Repeat("n", 64)})
		if i%32 == 31 {
			waitWritten(t, many, uint64(i+1))
		}
	}
	many.Close()
	if _, err = os.Stat(filepath.Join(root, "many-names")); !os.IsNotExist(err) {
		t.Fatal("large name-only history retained", err)
	}
	for _, id := range []string{"closed", "failed"} {
		s, err = Open(root, id, 0)
		if err != nil {
			t.Fatal(err)
		}
		e := core.Event{Kind: "session", Session: "s1", Status: core.Result{Connected: true}}
		if id == "failed" {
			e.Status = core.Result{Error: "connect failed"}
		}
		s.Emit(e)
		s.Emit(core.Event{Kind: "session", Session: "s1", Disposed: true, Status: core.Result{Error: "disconnected"}})
		s.Close()
	}
	list, err = reader.Instances()
	if err != nil || len(list) != 2 {
		t.Fatal(list, err)
	}
	for _, item := range list {
		for err = reader.Rebuild(item.ID); err == ErrIndexing; err = reader.Rebuild(item.ID) {
		}
		records, _, err := reader.Catalog(item.ID, 0, 10)
		if err != nil || len(records) != 1 {
			t.Fatal(records, err)
		}
		if records[0].Event.EverConnected != (item.ID == "closed") {
			t.Fatal("lost prior connection state", records)
		}
	}
}

func TestCrashEmptyCleanupAndUncertainRetention(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestEmptyOwnerHelper$")
	cmd.Env = append(os.Environ(), "KEEPSESS_EMPTY_OWNER_TEST="+root)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(out)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatal("child failed to start", scanner.Err())
	}
	reader, err := View(root, ".observer")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if list, err := reader.Instances(); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	if _, err = os.Stat(filepath.Join(root, "crashed")); err != nil {
		t.Fatal("disconnected live owner removed", err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	// Without a final drained snapshot the dead writer's pending events are
	// uncertain, so a crash alone must not delete its directory.
	_, _ = reader.Instances()
	if _, err = os.Stat(filepath.Join(root, "crashed")); err != nil {
		t.Fatal("crash with uncertain pending history removed", err)
	}
	// Simulate a crash after final drain but before directory cleanup.
	dir := filepath.Join(root, "crashed")
	if err = writeJSON(filepath.Join(dir, "state.json"), State{LastSeq: 1, WrittenSeq: 1, DurableSeq: 1}); err != nil {
		t.Fatal(err)
	}
	if list, err := reader.Instances(); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("dead empty owner not removed", err)
	}
	for _, id := range []string{"legacy", "corrupt", "gap", "logsession"} {
		dir = filepath.Join(root, id)
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		empty := false
		meta := Instance{ID: id, HasSessions: &empty}
		if id == "legacy" {
			meta.HasSessions = nil
		} else {
			if err = os.WriteFile(filepath.Join(dir, "owner.lock"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err = writeJSON(filepath.Join(dir, "instance.json"), meta); err != nil {
			t.Fatal(err)
		}
		if id == "corrupt" {
			if err = os.WriteFile(filepath.Join(dir, "broken.jsonl"), []byte("{broken"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if id == "gap" {
			if err = writeJSON(filepath.Join(dir, "state.json"), State{LastSeq: 1, LostEvents: 1}); err != nil {
				t.Fatal(err)
			}
		}
		if id == "logsession" {
			b, _ := json.Marshal(core.Event{Instance: id, Seq: 1, Kind: "connecting", Session: "s1"})
			if err = os.WriteFile(filepath.Join(dir, "00000000000000000001.jsonl"), append(b, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	list, err := reader.Instances()
	if err != nil || len(list) != 3 {
		t.Fatal(list, err)
	}
	for _, id := range []string{"legacy", "corrupt", "gap", "logsession"} {
		if _, err = os.Stat(filepath.Join(root, id)); err != nil {
			t.Fatal("uncertain/legacy history removed", id, err)
		}
	}
}
