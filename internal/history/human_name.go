package history

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Human labels live separately from instance.json, which belongs to the agent's
// journal writer. Browser edits must never race with or overwrite AI labels.
type humanLabel struct {
	Name string `json:"human_name"`
}

func (s *Store) RenameHumanInstance(instance, name string) error {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 64 || strings.TrimSpace(name) != name || strings.IndexFunc(name, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return errors.New("human_name must be empty or 1..64 printable Unicode characters without surrounding whitespace")
	}
	dir, err := s.instanceDir(instance)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, "instance.json"))
	if err != nil {
		return errors.New("unknown instance; refresh the instance list")
	}
	var info Instance
	if json.Unmarshal(b, &info) != nil || info.ID != instance {
		return errors.New("invalid instance metadata")
	}
	b, err = json.Marshal(humanLabel{Name: name})
	if err != nil {
		return err
	}
	// Unique temporary files also make concurrent browser saves safe (last wins).
	f, err := os.CreateTemp(dir, ".human-name-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, "human-name.json"))
}
