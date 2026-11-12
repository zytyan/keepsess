package history

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zytyan/keepsess/internal/core"
)

// Unknown or damaged histories are retained. Empty histories are small: cap the
// inspection rather than scanning an arbitrary archive during directory polling.
func sessionPresence(dir string, meta Instance) (has, known bool) {
	return inspectSessions(dir, meta, false)
}

func inspectSessions(dir string, meta Instance, strict bool) (has, known bool) {
	if meta.HasSessions != nil && *meta.HasSessions {
		return true, true
	}
	f, err := os.Open(dir)
	if err != nil {
		return false, false
	}
	defer f.Close()
	entries, err := f.ReadDir(129)
	if (err != nil && err != io.EOF) || len(entries) > 128 {
		return false, false
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var state State
	stateFound := false
	var records uint64
	remaining := int64(64 << 10)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return false, false
		}
		name := entry.Name()
		path := filepath.Join(dir, name)
		if strings.HasSuffix(name, ".meta") {
			return true, true
		}
		switch {
		case name == "instance.json" || name == "owner.lock":
		case name == "state.json":
			data, err := readSmall(path, 16<<10)
			if err != nil || json.Unmarshal(data, &state) != nil {
				return false, false
			}
			stateFound = true
			if state.Error != "" || state.ConnectionError != "" || state.LostEvents != 0 {
				return false, false
			}
		case strings.HasSuffix(name, ".jsonl"):
			data, err := readSmall(path, remaining)
			if err != nil {
				return false, false
			}
			remaining -= int64(len(data))
			scanner := bufio.NewScanner(strings.NewReader(string(data)))
			scanner.Buffer(make([]byte, 4096), 64<<10)
			for scanner.Scan() {
				var event core.Event
				if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Instance != meta.ID || event.Seq != records+1 {
					return false, false
				}
				if event.Session != "" || event.Handle != "" {
					return true, true
				}
				if event.Kind != "instance" {
					return false, false
				}
				records++
			}
			if scanner.Err() != nil {
				return false, false
			}
		default:
			return false, false
		}
	}
	// Only a final drained snapshot proves completeness. A periodic live
	// snapshot cannot rule out events that were still queued when the owner died.
	if strict && (!stateFound || state.Recording) {
		return false, false
	}
	if records > 0 && !stateFound && (strict || meta.HasSessions == nil) {
		return false, false
	}
	if (strict || meta.HasSessions == nil) && stateFound && (state.LastSeq != records || state.WrittenSeq != records || state.DurableSeq != records) {
		return false, false
	}
	return false, true
}

func readSmall(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = io.ErrShortBuffer
	}
	return data, err
}

// A successfully acquired existing owner lock proves that no writer still owns
// this directory. Link loss to the daemon alone must never trigger deletion.
// Legacy directories without this lock are only filtered, never removed.
func cleanupEmpty(dir string) bool {
	owner, err := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_RDWR, 0600)
	if err != nil {
		return false
	}
	if lockOwner(owner) != nil {
		owner.Close()
		return false
	}
	data, err := readSmall(filepath.Join(dir, "instance.json"), 16<<10)
	var meta Instance
	empty := false
	if err == nil && json.Unmarshal(data, &meta) == nil && meta.ID == filepath.Base(dir) {
		has, known := inspectSessions(dir, meta, true)
		empty = known && !has
	}
	// Windows cannot remove an open lock file. Existing directories can never be
	// reopened by writers (Open uses exclusive Mkdir), so release before removal.
	owner.Close()
	return empty && os.RemoveAll(dir) == nil
}

// A locked owner plus its explicit false presence bit hides a live empty
// instance even while its name-only journal is being written or exceeds a page.
func ownerActive(dir string) bool {
	f, err := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_RDWR, 0600)
	if err != nil {
		return false
	}
	defer f.Close()
	return lockOwner(f) != nil
}
