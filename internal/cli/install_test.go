package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
)

func plainJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	v, err := hujson.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	v.Standardize()
	var root map[string]any
	if err = json.Unmarshal(v.Pack(), &root); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestInstallJSONPreservesCommentsServersAndArguments(t *testing.T) {
	original := []byte(`{
 // keep this comment
 "inputs": [{"id":"token","type":"promptString"}],
 "servers": {
   "other": {"type":"http","url":"https://example.invalid/mcp"},
   "keepsess": {"command":"/old/keepsess","args":["serve","--log","/private/audit.jsonl"],"env":{"KEEP":"yes"},"disabled":true},
 },
}`)
	next, err := installJSON(original, "vscode", "/new path/keepsess")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(next, []byte("keep this comment")) {
		t.Fatal("lost comment")
	}
	root := plainJSON(t, next)
	servers := root["servers"].(map[string]any)
	server := servers["keepsess"].(map[string]any)
	if server["command"] != "/new path/keepsess" || server["type"] != "stdio" || server["disabled"] != true || server["env"].(map[string]any)["KEEP"] != "yes" || len(server["args"].([]any)) != 3 || servers["other"] == nil || root["inputs"] == nil {
		t.Fatal(root)
	}
	again, err := installJSON(next, "vscode", "/new path/keepsess")
	if err != nil || !bytes.Equal(next, again) {
		t.Fatal("not idempotent", err)
	}
	for _, bad := range []string{`[]`, `{"mcpServers":null}`, `{"mcpServers":{"keepsess":{"command":"other","args":["serve"]}}}`, `{"a":1,"a":2}`, `{"mcpServers":`} {
		if _, err = installJSON([]byte(bad), "json", "/bin/keepsess"); err == nil {
			t.Fatal("invalid/conflicting config accepted", bad)
		}
	}
}
func TestInstallTOMLPreservesAllOtherBytes(t *testing.T) {
	original := []byte("# top comment\nmodel = 'example'\ntext = '''\n[fake.table]\ncommand = 'not really a command'\n'''\n[mcp_servers.other]\ncommand = 'other-server'\n[mcp_servers.\"keepsess\"]\ncommand = '/old/keepsess' # keep inline comment\nargs = ['serve', '--log', '/private/audit.jsonl']\nenabled = false\n[mcp_servers.keepsess.env]\nKEEP = 'secret'\n[projects.'a.b']\ntrust_level = 'trusted'\n")
	next, err := installTOML(original, `C:\Program Files\keepsess\keepsess.exe`)
	if err != nil {
		t.Fatal(err)
	}
	oldCommand := []byte("'/old/keepsess'")
	parts := bytes.SplitN(original, oldCommand, 2)
	if !bytes.HasPrefix(next, parts[0]) || !bytes.HasSuffix(next, parts[1]) {
		t.Fatal("changed unrelated TOML bytes", string(next))
	}
	var root map[string]any
	if err = toml.Unmarshal(next, &root); err != nil {
		t.Fatal(err)
	}
	entry := root["mcp_servers"].(map[string]any)["keepsess"].(map[string]any)
	if entry["command"] != `C:\Program Files\keepsess\keepsess.exe` || entry["enabled"] != false || entry["env"].(map[string]any)["KEEP"] != "secret" {
		t.Fatal(root)
	}
	again, err := installTOML(next, `C:\Program Files\keepsess\keepsess.exe`)
	if err != nil || !bytes.Equal(again, next) {
		t.Fatal("not idempotent", err)
	}
	fresh, err := installTOML([]byte("# preserve\nmodel='example'\n"), "/new/keepsess")
	if err != nil || !bytes.HasPrefix(fresh, []byte("# preserve\nmodel='example'\n")) {
		t.Fatal(string(fresh), err)
	}
	if err = toml.Unmarshal(fresh, &root); err != nil {
		t.Fatal(err)
	}
	entry = root["mcp_servers"].(map[string]any)["keepsess"].(map[string]any)
	if entry["tool_timeout_sec"] != int64(120) {
		t.Fatal(entry)
	}
	inline := []byte(`mcp_servers = { keepsess = { command = '/old/keepsess', args = ['serve'] } }`)
	if _, err = installTOML(inline, "/new/keepsess"); err != nil {
		t.Fatal("inline existing entry", err)
	}
	if _, err = installTOML([]byte("[mcp_servers.keepsess]\ncommand='other'\nargs=['serve']\n"), "/new/keepsess"); err == nil {
		t.Fatal("collision overwritten")
	}
}
func TestInstallerCopiesBinarySkillAndConfigIdempotently(t *testing.T) {
	for _, client := range []string{"claude", "cursor", "vscode", "codex", "json"} {
		t.Run(client, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			if err := os.WriteFile(source, []byte("fake executable"), 0755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, "config.json")
			if client == "codex" {
				file = filepath.Join(root, ".codex", "config.toml")
			}
			opts := installOptions{client: client, file: file}
			var out bytes.Buffer
			if err := installAt(opts, source, &out); err != nil {
				t.Fatal(err)
			}
			executable := extractInstallPath(out.String(), "Executable: ")
			skill := extractInstallPath(out.String(), "Skill: ")
			b, err := os.ReadFile(executable)
			if err != nil || string(b) != "fake executable" {
				t.Fatal(string(b), err)
			}
			b, err = os.ReadFile(skill)
			if err != nil || string(b) != Skill {
				t.Fatal("skill", err)
			}
			first, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			out.Reset()
			if err = installAt(opts, source, &out); err != nil {
				t.Fatal(err)
			}
			second, _ := os.ReadFile(file)
			if !bytes.Equal(first, second) || strings.Contains(out.String(), "Backup:") {
				t.Fatal("idempotent install changed files", out.String())
			}
			if err = os.WriteFile(source, []byte("upgraded executable"), 0755); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			if err = installAt(opts, source, &out); err != nil {
				t.Fatal(err)
			}
			if extractInstallPath(out.String(), "Executable: ") == executable {
				t.Fatal("upgrade overwrites running binary")
			}
			if !strings.Contains(out.String(), "Backup:") {
				t.Fatal("missing backup")
			}
			backups, _ := filepath.Glob(file + ".keepsess-backup-*")
			if len(backups) != 1 {
				t.Fatal(backups)
			}
			b, _ = os.ReadFile(backups[0])
			if !bytes.Equal(first, b) {
				t.Fatal("backup changed original")
			}
			if runtime.GOOS != "windows" {
				info, _ := os.Stat(backups[0])
				if info.Mode().Perm() != 0600 {
					t.Fatal("backup not private")
				}
			}
		})
	}
}
func extractInstallPath(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}
func TestInstallerDryRunAndConflictWriteNothing(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	_ = os.WriteFile(source, []byte("fake executable"), 0755)
	file := filepath.Join(root, "new", ".mcp.json")
	var out bytes.Buffer
	if err := installAt(installOptions{client: "auto", file: file, dryRun: true}, source, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(file)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dry-run created files", err)
	}
	file = filepath.Join(root, ".mcp.json")
	original := []byte(`{"mcpServers":{"keepsess":{"command":"other","args":[]}}}`)
	_ = os.WriteFile(file, original, 0600)
	if err := installAt(installOptions{client: "auto", file: file}, source, &out); err == nil {
		t.Fatal("collision not rejected")
	}
	b, _ := os.ReadFile(file)
	if !bytes.Equal(b, original) {
		t.Fatal("collision changed config")
	}
	if _, err := os.Stat(filepath.Join(root, ".keepsess")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("collision created managed files")
	}
}
func TestInstallerRollbackOnConfigWriteFailure(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, "SKILL.md")
	original := []byte("user's previous skill")
	_ = os.WriteFile(skill, original, 0600)
	files := []installFile{{path: skill, data: []byte("new skill"), old: original, existed: true, changed: true, mode: 0600}, {path: filepath.Join(root, "config.json"), data: []byte("{}"), changed: true, mode: 0600}}
	err := commitInstall(files, func(path string, data []byte, mode os.FileMode) error {
		if path == files[1].path {
			return errors.New("injected write failure")
		}
		return atomicInstallWrite(path, data, mode)
	})
	if err == nil {
		t.Fatal("failure ignored")
	}
	b, _ := os.ReadFile(skill)
	if !bytes.Equal(b, original) {
		t.Fatal("skill not rolled back")
	}
	if _, err = os.Stat(files[1].path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed config created")
	}
}
func TestInstallAutoFormatAndFlagOrder(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "mcp.json")
	_ = os.WriteFile(file, []byte(`{"servers":{}}`), 0600)
	paths, err := resolveInstall(installOptions{client: "auto", file: file}, "hash")
	if err != nil || paths.client != "vscode" {
		t.Fatal(paths, err)
	}
	paths, err = resolveInstall(installOptions{client: "auto", file: filepath.Join(root, ".codex", "config.toml")}, "hash")
	if err != nil || paths.client != "codex" || paths.skill != filepath.Join(root, ".agents", "skills", "keepsess", "SKILL.md") {
		t.Fatal(paths, err)
	}
	for _, args := range [][]string{{"install", file, "--client", "vscode", "--dry-run"}, {"install", "--client=vscode", "--dry-run", file}} {
		var out bytes.Buffer
		if err = Run(context.Background(), args, nil, &out, &out); err != nil || !strings.Contains(out.String(), "Dry run") {
			t.Fatal(args, err, out.String())
		}
	}
}
