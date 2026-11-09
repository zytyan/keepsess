package settings

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// Appearance changes only the human Web projection, never terminal bytes.
type Appearance struct {
	Enabled bool            `toml:"enabled" json:"enabled"`
	Rules   []HighlightRule `toml:"rules" json:"rules"`
}
type HighlightRule struct {
	Name          string `toml:"name" json:"name"`
	Pattern       string `toml:"pattern" json:"pattern"`
	Regex         bool   `toml:"regex" json:"regex"`
	CaseSensitive bool   `toml:"case_sensitive" json:"case_sensitive"`
	Enabled       bool   `toml:"enabled" json:"enabled"`
	Foreground    string `toml:"foreground" json:"foreground"`
	Background    string `toml:"background" json:"background"`
	OverrideANSI  bool   `toml:"override_ansi" json:"override_ansi"`
}

func DefaultAppearance() Appearance {
	rule := func(name, pattern, color string) HighlightRule {
		return HighlightRule{Name: name, Pattern: pattern, Regex: true, Enabled: true, Foreground: color}
	}
	return Appearance{Enabled: true, Rules: []HighlightRule{
		rule("错误", `\b(errors?|failed|failure|fatal|denied|critical)\b`, "#ff8585"),
		rule("警告", `\b(warn|warning)\b`, "#ffd479"),
		rule("链路异常", `\b(?:link|protocol|state|status|is)\s+(?:is\s+)?down\b`, "#ff8585"),
		rule("链路正常", `\b(?:link|protocol|state|status|is)\s+(?:is\s+)?up\b`, "#83e6b0"),
		rule("IPv4", `\b(?:(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])\b`, "#88d8ff"),
		rule("MAC", `\b(?:[0-9a-f]{2}:){5}[0-9a-f]{2}\b|\b[0-9a-f]{4}(?:\.[0-9a-f]{4}){2}\b`, "#c8b0ff"),
		rule("接口", `\b(?:GigabitEthernet|TenGigabitEthernet|FastEthernet|Ethernet|Loopback|Vlan|Port-channel|Gi|Te|Fa|Eth)\d+(?:[/.:-]\d+)*\b`, "#88d8ff"),
	}}
}

var appearanceColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (a Appearance) Validate() error {
	if len(a.Rules) > 32 {
		return errors.New("最多 32 条高亮规则")
	}
	for i, r := range a.Rules {
		if len(r.Name) == 0 || len(r.Name) > 128 || len(r.Pattern) == 0 || len(r.Pattern) > 512 {
			return fmt.Errorf("规则 %d: 名称须为 1..128 字节，匹配表达式须为 1..512 字节", i+1)
		}
		if r.Foreground == "" && r.Background == "" {
			return fmt.Errorf("规则 %d: 至少指定一种颜色", i+1)
		}
		for _, c := range []string{r.Foreground, r.Background} {
			if c != "" && !appearanceColor.MatchString(c) {
				return fmt.Errorf("规则 %d: 颜色须为 #RRGGBB", i+1)
			}
		}
	}
	return nil
}

var appearanceMu sync.Mutex

// SaveAppearance preserves unrelated values and comments. Unusual inline/dotted
// representations are refused instead of rewriting the user's whole file.
func SaveAppearance(home string, a Appearance) error {
	if err := a.Validate(); err != nil {
		return err
	}
	appearanceMu.Lock()
	defer appearanceMu.Unlock()
	p, err := filepath.EvalSymlinks(filepath.Join(home, ".keepsess.toml"))
	if err != nil {
		return err
	}
	original, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if len(original) > 64<<10 {
		return errors.New("configuration exceeds 64 KiB")
	}
	var before map[string]any
	if err = toml.Unmarshal(original, &before); err != nil {
		return err
	}
	var kept strings.Builder
	skip := false
	for _, line := range strings.SplitAfter(string(original), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			header := strings.TrimSpace(strings.SplitN(trimmed, "#", 2)[0])
			skip = header == "[web.appearance]" || header == "[[web.appearance.rules]]"
		}
		if !skip {
			kept.WriteString(line)
		}
	}
	section, err := toml.Marshal(struct {
		Web struct {
			Appearance Appearance `toml:"appearance"`
		} `toml:"web"`
	}{Web: struct {
		Appearance Appearance `toml:"appearance"`
	}{a}})
	if err != nil {
		return err
	}
	// The encoder emits [web] before its child table; do not redefine that table.
	section = bytes.TrimPrefix(section, []byte("[web]\n"))
	updated := append([]byte(strings.TrimRight(kept.String(), "\n")+"\n\n"), section...)
	var after map[string]any
	if err = toml.Unmarshal(updated, &after); err != nil {
		return errors.New("请先将高亮设置改为 [web.appearance] / [[web.appearance.rules]] 表格格式")
	}
	remove := func(m map[string]any) {
		if w, ok := m["web"].(map[string]any); ok {
			delete(w, "appearance")
		}
	}
	remove(before)
	remove(after)
	if !reflect.DeepEqual(before, after) {
		return errors.New("无法保留其他配置，请手动编辑 [web.appearance]")
	}
	if len(updated) > 64<<10 {
		return errors.New("configuration exceeds 64 KiB")
	}
	// Validate the resulting appearance as well, including legacy dotted inputs.
	var check struct {
		Web struct {
			Appearance Appearance `toml:"appearance"`
		} `toml:"web"`
	}
	checkErr := toml.Unmarshal(updated, &check)
	wantAppearance, _ := toml.Marshal(a)
	gotAppearance, _ := toml.Marshal(check.Web.Appearance)
	if checkErr != nil || !bytes.Equal(wantAppearance, gotAppearance) {
		return errors.New("请先移除内联 appearance 配置，使用 [web.appearance] 表格")
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".keepsess-appearance-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(updated); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	latest, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if !bytes.Equal(latest, original) {
		return errors.New("配置已被其他程序修改，请重新加载后保存")
	}
	return os.Rename(f.Name(), p)
}
