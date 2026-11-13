package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExplicitProfilesPersistWithoutLiveState(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".keepsess.toml")
	recipe := `[profiles.server]
protocol='ssh'
mode='exec'
host='target.internal'
port=22
username='user'
keyfile='~/id_ed25519'
[[profiles.server.ssh_jumps]]
host='jump.internal'
username='jump'
password='private-jump-password'
[profiles.switch]
protocol='telnet'
mode='stream'
host='switch.internal'
port=23
`
	if err := os.WriteFile(path, []byte(recipe), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		c, err := LoadHome(home)
		if err != nil {
			t.Fatal(err)
		}
		profiles, err := c.ConnectionProfiles()
		if err != nil {
			t.Fatal(err)
		}
		p := profiles["server"]
		if p.Keyfile != filepath.Join(home, "id_ed25519") || len(p.SSHJumps) != 1 || p.SSHJumps[0].Password != "private-jump-password" || profiles["switch"].Mode != "stream" {
			t.Fatal("profile mismatch")
		}
		baseline := c
		baseline.Profiles = nil
		if c.Fingerprint() != baseline.Fingerprint() {
			t.Fatal("private profile affected daemon fingerprint")
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadHome(home); err == nil || !strings.Contains(err.Error(), "0600") {
			t.Fatal(err)
		}
		os.Chmod(path, 0600)
	}
	for _, bad := range []string{
		strings.Replace(recipe, "mode='exec'", "mode='guess'", 1),
		strings.Replace(recipe, "[profiles.server]", "[profiles.last]", 1),
		recipe + "key_passphrase='private-passphrase'\n",
		strings.Replace(recipe, "mode='stream'", "mode='exec'", 1),
	} {
		os.WriteFile(path, []byte(bad), 0600)
		if _, err := LoadHome(home); err == nil {
			t.Fatal("invalid profile accepted")
		} else if strings.Contains(err.Error(), "private-passphrase") || strings.Contains(err.Error(), "private-jump-password") {
			t.Fatal("profile error disclosed credential")
		}
	}
}
