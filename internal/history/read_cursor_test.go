package history

import (
	"encoding/json"
	"fmt"
	"github.com/zytyan/keepsess/internal/core"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadContinuationAppendReplaceAndCapacity(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "continuation")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "00000000000000000001.jsonl")
	write := func(n int) {
		t.Helper()
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for i := 1; i <= n; i++ {
			data, _ := json.Marshal(core.Event{Seq: uint64(i), Kind: "output", Session: "s1", Data: []byte(fmt.Sprintf("row %d 中文", i))})
			if _, err = f.Write(append(data, '\n')); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(400)
	s := &Store{root: root, instance: "continuation"}
	var after uint64
	for i := 1; i <= 400; i++ {
		p, err := s.Read("continuation", after, 1)
		if err != nil || len(p.Events) != 1 || p.Events[0].Seq != uint64(i) {
			t.Fatalf("page %d: %+v %v", i, p, err)
		}
		after = p.Next
	}
	if len(s.readCursors) > readCursorLimit {
		t.Fatal("unbounded read positions", len(s.readCursors))
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(core.Event{Seq: 401, Kind: "output", Session: "s1", Data: []byte("appended")})
	_, err = f.Write(append(data, '\n'))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Read("continuation", 400, 1)
	if err != nil || p.Next != 401 {
		t.Fatal(p, err)
	}
	// Re-reading from the beginning and independent cursors retain exact content.
	cold := &Store{root: root, instance: "continuation"}
	a, err := s.Read("continuation", 0, 12)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cold.Read("continuation", 0, 12)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("cursor changed history", err)
	}
	// A truncated file must invalidate an offset past its new EOF.
	write(1)
	p, err = s.Read("continuation", 400, 1)
	if err != nil || len(p.Events) != 0 {
		t.Fatal(p, err)
	}
	// Replacement with a distinct file identity also invalidates a cached position.
	if err = os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	write(4)
	p, err = s.Read("continuation", 1, 1)
	if err != nil || p.Next != 2 {
		t.Fatal(p, err)
	}
}
