package daemon_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/daemon"
	"github.com/zytyan/keepsess/internal/settings"
	"github.com/zytyan/keepsess/internal/web"
)

func TestUpgradeRealBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("builds real CLI binaries")
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if e := os.Mkdir(home, 0700); e != nil {
		t.Fatal(e)
	}
	cfg, e := settings.LoadHome(home)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := func(name string) string {
		filename := name
		if runtime.GOOS == "windows" {
			filename += ".exe"
		}
		p := filepath.Join(dir, filename)
		cmd := exec.CommandContext(ctx, "go", "build", "-o", p, "-ldflags=-X github.com/zytyan/keepsess/internal/buildinfo.Revision="+name, "../../cmd/keepsess")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("build: %v %s", e, b)
		}
		return p
	}
	old, newer := build("old"), build("new")
	run := func(p string, args ...string) daemon.Runtime {
		cmd := exec.CommandContext(ctx, p, append([]string{"daemon"}, args...)...)
		cmd.Env = append(os.Environ(), "KEEPSESS_SETTINGS_HOME="+home)
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("%v: %v %s", args, e, b)
		}
		var r daemon.Runtime
		if e = json.Unmarshal(b, &r); e != nil {
			t.Fatalf("decode %s: %v", b, e)
		}
		if r.Token != "" || r.Endpoint != "" {
			t.Fatal("credentials in result")
		}
		return r
	}
	defer func() { _ = daemon.Stop(context.Background(), cfg) }()

	// Bootstrap through the old v1 authenticated control surface.
	var legacy *httptest.Server
	legacy = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("ab", 32) {
			http.Error(w, "forbidden", 403)
			return
		}
		switch r.URL.Path {
		case "/internal/info":
			_ = json.NewEncoder(w).Encode(web.Info{Enabled: true, Listen: strings.TrimPrefix(legacy.URL, "http://"), BasePath: "/"})
		case "/internal/stop":
			w.WriteHeader(200)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			go legacy.Close()
		default:
			http.NotFound(w, r)
		}
	}))
	defer legacy.Close()
	if e = os.MkdirAll(cfg.RuntimeDir(), 0700); e != nil {
		t.Fatal(e)
	}
	legacyRuntime := daemon.Runtime{Protocol: 1, Token: strings.Repeat("ab", 32), Endpoint: legacy.URL + "/internal/", Info: web.Info{Listen: strings.TrimPrefix(legacy.URL, "http://"), BasePath: "/"}}
	raw, _ := json.Marshal(legacyRuntime)
	if e = os.WriteFile(filepath.Join(cfg.RuntimeDir(), "daemon.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	a := run(old, "upgrade")
	if a.StartID == "" || a.BinarySHA256 == "" {
		t.Fatal("missing identity")
	}

	// Preflight failure must leave the existing observer running.
	configPath := filepath.Join(home, ".keepsess.toml")
	original, e := os.ReadFile(configPath)
	if e != nil {
		t.Fatal(e)
	}
	disabled := strings.Replace(string(original), "enabled = true", "enabled = false", 1)
	if e = os.WriteFile(configPath, []byte(disabled), 0600); e != nil {
		t.Fatal(e)
	}
	bad := exec.CommandContext(ctx, newer, "daemon", "upgrade")
	bad.Env = append(os.Environ(), "KEEPSESS_SETTINGS_HOME="+home)
	if _, e = bad.CombinedOutput(); e == nil {
		t.Fatal("disabled candidate preflight passed")
	}
	if e = os.WriteFile(configPath, original, 0600); e != nil {
		t.Fatal(e)
	}
	if still := run(old, "status"); still.StartID != a.StartID {
		t.Fatal("preflight stopped old daemon")
	}
	b := run(newer, "upgrade")
	if a.StartID == b.StartID || a.BinarySHA256 == b.BinarySHA256 || a.Info.Listen != b.Info.Listen {
		t.Fatalf("bad replacement: %#v %#v", a, b)
	}
	c := run(newer, "upgrade")
	if c.StartID != b.StartID {
		t.Fatal("same binary restarted")
	}
	_ = run(newer, "status")
	if e = daemon.Stop(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	// An older Agent's launcher must honor the selected new binary after stop.
	time.Sleep(100 * time.Millisecond)
	// Direct Ensure/Launch exercise uses the persisted selection, independent of caller binary.
	r, e := daemon.Ensure(ctx, cfg, daemon.Launch)
	if e != nil {
		t.Fatal(e)
	}
	status, e := daemon.Status(ctx, r)
	if e != nil || status.BinarySHA256 != b.BinarySHA256 {
		t.Fatal("selected version lost", status, e)
	}
}
