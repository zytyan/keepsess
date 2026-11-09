// Package settings owns the single per-user keepsess configuration.
package settings

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	AuditLog string `toml:"audit_log"`
	Tools    struct {
		Profile string `toml:"profile"`
	} `toml:"tools"`
	Web struct {
		Enabled   bool   `toml:"enabled"`
		Listen    string `toml:"listen"`
		BasePath  string `toml:"base_path"`
		PublicURL string `toml:"public_url"`
	} `toml:"web"`
	History struct {
		Dir      string `toml:"dir"`
		MaxBytes int64  `toml:"max_bytes"`
	} `toml:"history"`
	Home string `toml:"-"`
}

const Template = `# keepsess user configuration. Agent command: keepsess serve
# Changes to Web/history settings take effect after: keepsess daemon stop
# The next serve starts the daemon again. Existing SSH/Telnet connections stay
# in their Agent processes and are never replayed.
# audit_log = "" # Optional JSONL audit; terminal commands may contain secrets.

[tools]
profile = "standard" # 18 daily tools; "full" exposes all 30.

[web]
enabled = true
listen = "127.0.0.1:8765"
base_path = "/"
public_url = ""

[history]
# Empty means ~/.keepsess/history (existing legacy history is reused).
# Input/output may contain passwords.
dir = ""
max_bytes = 0 # No automatic deletion; 0 means unlimited disk capacity.
`

func Load() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	return LoadHome(home)
}
func LoadHome(home string) (Config, error) {
	var c Config
	c.Home = home
	c.Tools.Profile = "standard"
	c.Web.Enabled = true
	c.Web.Listen = "127.0.0.1:8765"
	c.Web.BasePath = "/"
	p := filepath.Join(home, ".keepsess.toml")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		// Publish a complete template without replacing a file created by another
		// simultaneous Agent (or by the user). O_EXCL on the destination alone
		// would expose an empty/partial TOML file to concurrent readers.
		f, e := os.CreateTemp(home, ".keepsess-config-*.tmp")
		if e != nil {
			return c, e
		}
		tmp := f.Name()
		defer os.Remove(tmp)
		_, e = f.WriteString(Template)
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return c, e
		}
		if e = os.Link(tmp, p); e != nil && !os.IsExist(e) {
			return c, fmt.Errorf("create %s: %w", p, e)
		}
		b, err = os.ReadFile(p)
	}
	if err != nil {
		return c, err
	}
	if len(b) > 64<<10 {
		return c, fmt.Errorf("%s exceeds 64 KiB", p)
	}
	if err = toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&c); err != nil {
		return c, fmt.Errorf("%s: %w", p, err)
	}
	c.Home = home
	if c.Tools.Profile != "standard" && c.Tools.Profile != "full" {
		return c, fmt.Errorf("tools.profile must be standard or full")
	}
	c.History.Dir = expand(home, c.History.Dir)
	if c.History.Dir == "" {
		c.History.Dir = filepath.Join(home, ".keepsess", "history")
		// Existing installations keep their journal location so upgrading the Web
		// architecture does not hide any retained history.
		if actual, e := os.UserHomeDir(); e == nil && filepath.Clean(actual) == filepath.Clean(home) {
			if oldBase, e := os.UserConfigDir(); e == nil {
				old := filepath.Join(oldBase, "keepsess", "history")
				if st, e := os.Stat(old); e == nil && st.IsDir() {
					c.History.Dir = old
				}
			}
		}
	}
	c.AuditLog = expand(home, c.AuditLog)
	if !filepath.IsAbs(c.History.Dir) || c.AuditLog != "" && !filepath.IsAbs(c.AuditLog) {
		return c, fmt.Errorf("history.dir and audit_log must be absolute paths or start with ~/")
	}
	if c.History.MaxBytes < 0 {
		return c, fmt.Errorf("history.max_bytes must be nonnegative")
	}
	return c, nil
}
func expand(home, s string) string {
	if s == "~" {
		return home
	}
	if strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		return filepath.Join(home, s[2:])
	}
	return s
}
func (c Config) RuntimeDir() string { return filepath.Join(c.Home, ".keepsess") }
func (c Config) Fingerprint() string {
	b, _ := toml.Marshal(struct {
		Web     any `toml:"web"`
		History any `toml:"history"`
	}{c.Web, c.History})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
