package history

import (
	"encoding/json"
	"fmt"
	"github.com/zytyan/keepsess/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkSequentialReplay(b *testing.B) {
	root := b.TempDir()
	dir := filepath.Join(root, "bench")
	if err := os.MkdirAll(dir, 0700); err != nil {
		b.Fatal(err)
	}
	var file *os.File
	var size int
	for i := 1; i <= 12000; i++ {
		if file == nil || size >= segmentBytes {
			if file != nil {
				file.Close()
			}
			var err error
			file, err = os.Create(filepath.Join(dir, fmt.Sprintf("%020d.jsonl", i)))
			if err != nil {
				b.Fatal(err)
			}
			size = 0
		}
		data, err := json.Marshal(core.Event{Seq: uint64(i), Kind: "output", Session: "s1", Data: []byte(strings.Repeat("history 中文 ", 70))})
		if err != nil {
			b.Fatal(err)
		}
		data = append(data, '\n')
		if _, err = file.Write(data); err != nil {
			b.Fatal(err)
		}
		size += len(data)
	}
	file.Close()
	store := &Store{root: root, instance: "bench"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		after := uint64(0)
		count := 0
		for {
			page, err := store.Read("bench", after, 4096)
			if err != nil {
				b.Fatal(err)
			}
			count += len(page.Events)
			if !page.More {
				break
			}
			if page.Next <= after {
				b.Fatal("no replay progress")
			}
			after = page.Next
		}
		if count != 12000 {
			b.Fatal(count)
		}
	}
}
