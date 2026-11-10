package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsPrivateTemplateAndPartialConfig(t *testing.T) {
	home := t.TempDir()
	c, err := LoadHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Web.Enabled || c.Web.Listen != "127.0.0.1:8765" || c.History.Dir != filepath.Join(home, ".keepsess", "history") {
		t.Fatal(c)
	}
	st, err := os.Stat(filepath.Join(home, ".keepsess.toml"))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, err)
	}
	if strings.Contains(Template, "[tools]") {
		t.Fatal("obsolete discovery profile in new config template")
	}
	p := filepath.Join(home, ".keepsess.toml")
	text := "audit_log='~/audit.jsonl'\n[web]\nbase_path='/console/'\n[history]\ndir='~/terminal-data'\nmax_bytes=12345\n"
	if err = os.WriteFile(p, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = LoadHome(home)
	if err != nil || !c.Web.Enabled || c.History.Dir != filepath.Join(home, "terminal-data") || c.AuditLog != filepath.Join(home, "audit.jsonl") {
		t.Fatal(c, err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != text {
		t.Fatal("existing user config overwritten")
	}
	for _, text := range []string{"[web]\nlistne='oops'", "[history]\nmax_bytes=-1", "[history]\ndir='relative'", "[web]\nenabled='false'"} {
		os.WriteFile(p, []byte(text), 0600)
		if _, err = LoadHome(home); err == nil {
			t.Fatal("invalid config accepted", text)
		}
	}
	if strings.Contains(c.Fingerprint(), home) {
		t.Fatal("fingerprint leaks path")
	}
}

func TestReuseLegacyHistoryAndIgnoreObsoleteToolProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("APPDATA", filepath.Join(home, "config"))
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(base, "keepsess", "history")
	if err = os.MkdirAll(old, 0700); err != nil {
		t.Fatal(err)
	}
	c, err := LoadHome(home)
	if err != nil || c.History.Dir != old {
		t.Fatal("legacy history hidden", c, err)
	}
	before := c.Fingerprint()
	c.Tools.Profile = "full"
	c.AuditLog = filepath.Join(home, "audit.jsonl")
	if c.Fingerprint() != before {
		t.Fatal("Agent-only preferences trigger daemon restart")
	}
	c.Web.BasePath = "/different/"
	if c.Fingerprint() == before {
		t.Fatal("daemon fingerprint ignores Web settings")
	}
	if err = os.WriteFile(filepath.Join(home, ".keepsess.toml"), []byte("[tools]\nprofile='full'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = LoadHome(home)
	if err != nil || c.Tools.Profile != "full" {
		t.Fatal(c, err)
	}
	if err = os.WriteFile(filepath.Join(home, ".keepsess.toml"), []byte("[tools]\nprofile='typo'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadHome(home); err != nil {
		t.Fatal("obsolete profile must be ignored", err)
	}
}

func TestConcurrentFirstConfigCreation(t *testing.T) {
	home := t.TempDir()
	errors := make(chan error, 32)
	for i := 0; i < 32; i++ {
		go func() { _, err := LoadHome(home); errors <- err }()
	}
	for i := 0; i < 32; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(home, ".keepsess.toml"))
	if err != nil || string(b) != Template {
		t.Fatal("partial or overwritten template", err)
	}
}
