package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/zytyan/keepsess/internal/settings"
)

type selection struct {
	Current  string `json:"current"`
	Previous string `json:"previous,omitempty"`
}
type upgradeTicket struct {
	Candidate string `json:"candidate"`
	Previous  string `json:"previous,omitempty"`
	Token     string `json:"token"`
	Listen    string `json:"listen"`
	BasePath  string `json:"base_path"`
}

func digest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func ownDigest() (string, error) {
	p, e := os.Executable()
	if e != nil {
		return "", e
	}
	return digest(p)
}
func atomicJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".state-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
func readSelection(c settings.Config) selection {
	var s selection
	b, _ := os.ReadFile(filepath.Join(c.RuntimeDir(), "selected.json"))
	_ = json.Unmarshal(b, &s)
	return s
}
func versionPath(c settings.Config, hash string) string {
	name := "keepsess"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(c.RuntimeDir(), "versions", hash, name)
}
func selectedBinary(c settings.Config) (string, error) {
	var chosen selection
	b, e := os.ReadFile(filepath.Join(c.RuntimeDir(), "selected.json"))
	if e != nil && !os.IsNotExist(e) {
		return "", e
	}
	if e == nil {
		if e = json.Unmarshal(b, &chosen); e != nil {
			return "", fmt.Errorf("invalid daemon selection: %w", e)
		}
	}
	hash := chosen.Current
	if hash == "" {
		return os.Executable()
	}
	if len(hash) != 64 {
		return "", errors.New("invalid selected daemon digest")
	}
	if _, e := hex.DecodeString(hash); e != nil {
		return "", e
	}
	p := versionPath(c, hash)
	got, e := digest(p)
	if e != nil {
		return "", e
	}
	if got != hash {
		return "", errors.New("selected daemon digest mismatch")
	}
	return p, nil
}
func gate(c settings.Config) (*os.File, error) { return namedLock(c, "upgrade.lock") }
func namedLock(c settings.Config, name string) (*os.File, error) {
	if e := os.MkdirAll(c.RuntimeDir(), 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(c.RuntimeDir(), name), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = acquireLock(f); e != nil {
		f.Close()
		return nil, fmt.Errorf("daemon lifecycle busy: %w", e)
	}
	return f, nil
}
func pause(ctx context.Context) error {
	t := time.NewTimer(50 * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func waitGate(ctx context.Context, c settings.Config) (*os.File, error) {
	for {
		f, e := gate(c)
		if e == nil {
			return f, nil
		}
		if e = pause(ctx); e != nil {
			return nil, e
		}
	}
}
func ticketPath(c settings.Config) string { return filepath.Join(c.RuntimeDir(), "upgrade.json") }
func candidateTicket(c settings.Config) (upgradeTicket, bool) {
	var t upgradeTicket
	token := os.Getenv("KEEPSESS_UPGRADE_TOKEN")
	if token == "" {
		return t, false
	}
	b, _ := os.ReadFile(ticketPath(c))
	if json.Unmarshal(b, &t) != nil {
		return t, false
	}
	hash, e := ownDigest()
	return t, t.Token == token && e == nil && hash == t.Candidate
}
func probeIdentity(ctx context.Context, r Runtime) (Runtime, error) {
	response, e := localRequest(ctx, r, "GET", "status")
	if e != nil {
		return Runtime{}, e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return Runtime{}, fmt.Errorf("daemon status: HTTP %d", response.StatusCode)
	}
	var got Runtime
	e = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&got)
	got.Token = r.Token
	got.Endpoint = r.Endpoint
	return got, e
}
func stopRuntime(ctx context.Context, r Runtime) error {
	res, e := localRequest(ctx, r, "POST", "stop")
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("daemon stop: HTTP %d", res.StatusCode)
	}
	return nil
}

// Upgrade installs only the invoking binary. Remote sessions stay in Agents.
func Upgrade(ctx context.Context, c settings.Config) (Runtime, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	lock, e := gate(c)
	if e != nil {
		return Runtime{}, e
	}
	defer lock.Close()
	if e = recoverUpgrade(ctx, c); e != nil {
		return Runtime{}, e
	}
	exe, e := os.Executable()
	if e != nil {
		return Runtime{}, e
	}
	hash, e := digest(exe)
	if e != nil {
		return Runtime{}, e
	}
	old, readErr := readRuntime(c, false)
	live := false
	if readErr == nil {
		_, e = Probe(ctx, old)
		live = e == nil
		if live {
			if id, err := probeIdentity(ctx, old); err == nil && id.BinarySHA256 == hash {
				return id, nil
			}
		}
	}
	target := versionPath(c, hash)
	if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
		return Runtime{}, e
	}
	if got, err := digest(target); err != nil || got != hash {
		src, err := os.Open(exe)
		if err != nil {
			return Runtime{}, err
		}
		defer src.Close()
		f, err := os.CreateTemp(filepath.Dir(target), ".binary-*")
		if err != nil {
			return Runtime{}, err
		}
		defer os.Remove(f.Name())
		_, err = io.Copy(f, src)
		if err == nil {
			err = f.Chmod(0700)
		}
		if err == nil {
			err = f.Sync()
		}
		ce := f.Close()
		if err != nil {
			return Runtime{}, err
		}
		if ce != nil {
			return Runtime{}, ce
		}
		if err = os.Rename(f.Name(), target); err != nil {
			return Runtime{}, err
		}
	}
	if got, err := digest(target); err != nil || got != hash {
		return Runtime{}, errors.New("candidate digest verification failed")
	}
	preCtx, preCancel := context.WithTimeout(ctx, 5*time.Second)
	defer preCancel()
	pre := exec.CommandContext(preCtx, target, "daemon", "check")
	pre.Env = append(os.Environ(), "KEEPSESS_SETTINGS_HOME="+c.Home)
	if b, err := pre.CombinedOutput(); err != nil {
		return Runtime{}, fmt.Errorf("candidate preflight: %w: %.1024s", err, b)
	}
	previous := readSelection(c)
	ticket := upgradeTicket{Candidate: hash, Previous: previous.Current}
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return Runtime{}, e
	}
	ticket.Token = hex.EncodeToString(b)
	if live {
		ticket.Listen = old.Info.Listen
		ticket.BasePath = old.Info.BasePath
	}
	if e = atomicJSON(ticketPath(c), ticket); e != nil {
		return Runtime{}, e
	}
	defer os.Remove(ticketPath(c))
	if live {
		if e = stopRuntime(ctx, old); e != nil {
			return Runtime{}, e
		}
	}
	start := func(path, expected string) (Runtime, error) {
		// Wait for listener/lock release without sending any Agent operations.
		if live {
			for {
				if _, err := Probe(ctx, old); err != nil {
					break
				}
				if err := pause(ctx); err != nil {
					return Runtime{}, err
				}
			}
		}
		proc, err := launchProcess(c, path, []string{"KEEPSESS_UPGRADE_TOKEN=" + ticket.Token, "KEEPSESS_SETTINGS_HOME=" + c.Home})
		if err != nil {
			return Runtime{}, err
		}
		healthy := false
		defer func() {
			if !healthy {
				_ = proc.Kill()
			}
		}()
		waitCtx, stop := context.WithTimeout(ctx, 12*time.Second)
		defer stop()
		for {
			if r, err := readRuntime(c, false); err == nil {
				if id, err := probeIdentity(waitCtx, r); err == nil && id.BinarySHA256 == expected && (!live || id.StartID != old.StartID) {
					healthy = true
					return id, nil
				}
			}
			if err := pause(waitCtx); err != nil {
				return Runtime{}, err
			}
		}
	}
	next, err := start(target, hash)
	if err == nil {
		err = atomicJSON(filepath.Join(c.RuntimeDir(), "selected.json"), selection{Current: hash, Previous: previous.Current})
		if err == nil {
			pruneVersions(c, hash, previous.Current)
			return next, nil
		}
		_ = stopRuntime(ctx, next)
	}
	// Retain a usable previous version; never replay application requests.
	if previous.Current != "" {
		if path, pe := selectedBinary(c); pe == nil {
			ticket.Candidate = previous.Current
			if e := atomicJSON(ticketPath(c), ticket); e != nil {
				return Runtime{}, fmt.Errorf("upgrade failed: %v; recovery ticket: %w", err, e)
			}
			if _, re := start(path, previous.Current); re == nil {
				return Runtime{}, fmt.Errorf("upgrade failed; previous daemon restored: %w", err)
			} else {
				return Runtime{}, fmt.Errorf("upgrade failed: %v; recovery failed: %w", err, re)
			}
		}
	}
	return Runtime{}, fmt.Errorf("upgrade failed; no previous managed binary available for recovery: %w", err)
}

// Recover a committed healthy candidate after the CLI died, or discard a stale
// ticket before normal startup. Caller owns the lifecycle lock.
func recoverUpgrade(ctx context.Context, c settings.Config) error {
	lockCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var handoff *os.File
	for {
		var e error
		handoff, e = namedLock(c, "handoff.lock")
		if e == nil {
			break
		}
		if e = pause(lockCtx); e != nil {
			return e
		}
	}
	defer handoff.Close()
	b, e := os.ReadFile(ticketPath(c))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var t upgradeTicket
	if e = json.Unmarshal(b, &t); e != nil {
		return e
	}
	if r, err := readRuntime(c, false); err == nil {
		if id, err := probeIdentity(ctx, r); err == nil && id.BinarySHA256 == t.Candidate {
			if e = atomicJSON(filepath.Join(c.RuntimeDir(), "selected.json"), selection{Current: t.Candidate, Previous: t.Previous}); e != nil {
				return e
			}
		}
	}
	return os.Remove(ticketPath(c))
}

// Status returns authenticated daemon identity without private discovery credentials.
func Status(ctx context.Context, r Runtime) (Runtime, error) {
	v, e := probeIdentity(ctx, r)
	v.Token = ""
	v.Endpoint = ""
	return v, e
}

// Keep only managed current/previous images. Windows may retain a file still
// executing during shutdown; removal failures are harmless and retried later.
func pruneVersions(c settings.Config, current, previous string) {
	root := filepath.Join(c.RuntimeDir(), "versions")
	entries, e := os.ReadDir(root)
	if e != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) != 64 || name == current || name == previous {
			continue
		}
		if _, e = hex.DecodeString(name); e == nil {
			_ = os.RemoveAll(filepath.Join(root, name))
		}
	}
}
