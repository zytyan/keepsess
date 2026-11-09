package settings

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppearancePersistence(t *testing.T) {
	home := t.TempDir()
	c, err := LoadHome(home)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".keepsess.toml")
	original, _ := os.ReadFile(p)
	a := DefaultAppearance()
	a.Rules[0].Pattern = "自定义"
	a.Rules[0].Regex = false
	if err = SaveAppearance(home, a); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !bytes.Contains(b, original) {
		t.Fatal("unrelated configuration/comments changed")
	}
	c2, err := LoadHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Web.Appearance.Rules[0].Pattern != "自定义" {
		t.Fatal(c2.Web.Appearance)
	}
	if c.Fingerprint() != c2.Fingerprint() {
		t.Fatal("appearance changed daemon identity")
	}
	a.Enabled = false
	a.Rules = []HighlightRule{}
	if err = SaveAppearance(home, a); err != nil {
		t.Fatal(err)
	}
	c2, err = LoadHome(home)
	if err != nil || c2.Web.Appearance.Enabled || len(c2.Web.Appearance.Rules) != 0 {
		t.Fatal(c2.Web.Appearance, err)
	}
}
func TestAppearanceRejectsInvalidAndPreservesConfig(t *testing.T) {
	home := t.TempDir()
	_, _ = LoadHome(home)
	p := filepath.Join(home, ".keepsess.toml")
	original, _ := os.ReadFile(p)
	a := DefaultAppearance()
	a.Rules[0].Foreground = "red"
	if SaveAppearance(home, a) == nil {
		t.Fatal("invalid color")
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, original) {
		t.Fatal("changed invalid config")
	}
	inline := strings.Replace(string(original), "[web]", "[web]\nappearance = { enabled = true, rules = [] }", 1)
	if err := os.WriteFile(p, []byte(inline), 0600); err != nil {
		t.Fatal(err)
	}
	if SaveAppearance(home, DefaultAppearance()) == nil {
		t.Fatal("inline appearance must not be overwritten")
	}
	got, _ = os.ReadFile(p)
	if string(got) != inline {
		t.Fatal("inline configuration changed")
	}
}
