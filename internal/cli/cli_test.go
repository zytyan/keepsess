package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"mcp-skill"}, {"version"}} {
		var out bytes.Buffer
		if e := Run(context.Background(), args, strings.NewReader(""), &out, &out); e != nil || out.Len() == 0 {
			t.Fatal(args, e)
		}
	}
	dir := filepath.Join(t.TempDir(), "skill")
	var out bytes.Buffer
	if e := Run(context.Background(), []string{"install-skill", "--dir", dir}, nil, &out, &out); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if e != nil || string(b) != Skill {
		t.Fatal("skill mismatch", e)
	}
	if e = Run(context.Background(), []string{"install-skill", "--dir", dir}, nil, &out, &out); e == nil {
		t.Fatal("overwrote existing skill")
	}
}
func TestAuditRedaction(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	a, e := OpenAudit(p)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if e = a.Record(map[string]any{"arguments": map[string]any{
		"password": "password-secret", "key_passphrase": "key-secret", "command": "show version",
		"env": map[string]any{"SECRET_VALUE": "env-secret-value"},
		"ssh_jumps": []any{
			map[string]any{"host": "jump1", "password": "jump-password-secret"},
			map[string]any{"host": "jump2", "key_passphrase": "jump-passphrase-secret"},
		},
	}}); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "password-secret") || strings.Contains(string(b), "key-secret") || strings.Contains(string(b), "jump-passphrase-secret") || strings.Contains(string(b), "env-secret-value") || !strings.Contains(string(b), "show version") {
		t.Fatal(string(b))
	}
	var v any
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	st, _ := os.Stat(p)
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
}

func TestServeWebDiscoveryAndStdioIsolation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		extra   []string
		enabled bool
		failure bool
	}{
		{"default", nil, true, false},
		{"disabled", []string{"--web=false"}, false, false},
		{"invalid root keeps MCP alive", []string{"--web-base-path=/{invalid}/"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"serve", "--web-listen=127.0.0.1:0", "--history-dir=" + t.TempDir()}
			args = append(args, tc.extra...)
			input := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"sess_list\",\"arguments\":{}}}\n"
			var out, errout bytes.Buffer
			if e := Run(context.Background(), args, strings.NewReader(input), &out, &errout); e != nil {
				t.Fatal(e)
			}
			d := json.NewDecoder(&out)
			count := 0
			for d.More() {
				var response map[string]any
				if e := d.Decode(&response); e != nil {
					t.Fatal("non-MCP stdout", e)
				}
				result := response["result"].(map[string]any)
				var info map[string]any
				if response["id"] == float64(1) {
					info = result["_meta"].(map[string]any)["web"].(map[string]any)
				} else {
					info = result["structuredContent"].(map[string]any)["web"].(map[string]any)
				}
				if info["enabled"] != tc.enabled {
					t.Fatal(info)
				}
				if tc.enabled {
					if !strings.HasPrefix(info["url"].(string), "http://127.0.0.1:") {
						t.Fatal(info)
					}
				}
				if tc.failure && info["error"] == nil {
					t.Fatal(info)
				}
				count++
			}
			if count != 2 {
				t.Fatal("missing responses", count, errout.String())
			}
			if tc.enabled && !strings.Contains(errout.String(), "keepsess Web:") {
				t.Fatal(errout.String())
			}
		})
	}
}
