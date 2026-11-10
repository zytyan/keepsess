package history

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zytyan/keepsess/internal/core"
)

func TestHumanNameIndependentOfAgentAndSurvivesArchive(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "agent", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Emit(core.Event{Kind: "session", Session: "s1"})
	waitWritten(t, s, 1)
	view, err := View(root, ".daemon")
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if err := view.RenameHumanInstance("agent", "人类名称"); err != nil {
		t.Fatal(err)
	}
	s.Emit(core.Event{Kind: "instance", Name: "AI 更新名称"})
	waitWritten(t, s, 2)
	check := func(st *Store, want string) {
		t.Helper()
		list, err := st.Instances()
		if err != nil || len(list) != 1 {
			t.Fatalf("%+v %v", list, err)
		}
		if list[0].Name != "AI 更新名称" || list[0].HumanName != want {
			t.Fatal(list[0])
		}
	}
	check(view, "人类名称")
	for _, bad := range []string{" leading", "trailing ", "a\nb", strings.Repeat("中", 65), string([]byte{255})} {
		if err := view.RenameHumanInstance("agent", bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	for _, bad := range []string{"../agent", "missing", ""} {
		if err := view.RenameHumanInstance(bad, "name"); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	check(view, "人类名称")
	info, err := os.Stat(filepath.Join(root, "agent", "human-name.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	s.Close()
	restarted, err := View(root, ".daemon")
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	check(restarted, "人类名称")
	if err := restarted.RenameHumanInstance("agent", "归档名称"); err != nil {
		t.Fatal(err)
	}
	check(restarted, "归档名称")
	if err := restarted.RenameHumanInstance("agent", ""); err != nil {
		t.Fatal(err)
	}
	check(restarted, "")
}
