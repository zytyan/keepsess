// Package daemon joins all keepsess processes for the current OS user behind
// one persistent Web listener. Remote connections remain owned by each Agent.
package daemon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/zytyan/keepsess/internal/settings"
	"github.com/zytyan/keepsess/internal/web"
)

const protocol = 2

type Runtime struct {
	StartID         string   `json:"start_id,omitempty"`
	Version         string   `json:"version,omitempty"`
	BinarySHA256    string   `json:"binary_sha256,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	PID             int      `json:"pid"`
	Protocol        int      `json:"protocol"`
	Endpoint        string   `json:"endpoint,omitempty"`
	Token           string   `json:"token,omitempty"`
	HistoryDir      string   `json:"history_dir"`
	HistoryMaxBytes int64    `json:"history_max_bytes"`
	ConfigHash      string   `json:"config_hash"`
	Info            web.Info `json:"info"`
}

func runtimePath(c settings.Config) string { return filepath.Join(c.RuntimeDir(), "daemon.json") }
func ReadRuntime(c settings.Config) (Runtime, error) {
	return readRuntime(c, true)
}
func readRuntime(c settings.Config, compatible bool) (Runtime, error) {
	var r Runtime
	b, err := os.ReadFile(runtimePath(c))
	if err != nil {
		return r, err
	}
	if len(b) > 16<<10 {
		return r, errors.New("invalid daemon runtime file")
	}
	err = json.Unmarshal(b, &r)
	if err == nil && ((compatible && r.Protocol != protocol) || r.Token == "" || r.Endpoint == "") {
		err = errors.New("incompatible daemon runtime; restart keepsess daemon")
	}
	if err == nil {
		u, e := url.Parse(r.Endpoint)
		token, e2 := hex.DecodeString(r.Token)
		if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !isLocalIP(net.ParseIP(u.Hostname())) || e2 != nil || len(token) != 32 {
			err = errors.New("invalid local daemon endpoint or token")
		}
	}
	return r, err
}
func localRequest(ctx context.Context, r Runtime, method, endpoint string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, r.Endpoint+endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	return (&http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
}
func Probe(ctx context.Context, r Runtime) (web.Info, error) {
	var info web.Info
	response, err := localRequest(ctx, r, "GET", "info")
	if err != nil {
		return info, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return info, fmt.Errorf("daemon info: HTTP %d", response.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&info)
	return info, err
}
func Stop(ctx context.Context, c settings.Config) error {
	lock, err := gate(c)
	if err != nil {
		return err
	}
	defer lock.Close()
	r, err := readRuntime(c, false)
	if err != nil {
		return err
	}
	response, err := localRequest(ctx, r, "POST", "stop")
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("daemon stop: HTTP %d", response.StatusCode)
	}
	return nil
}

// Concurrent starters may launch several short-lived contenders. The OS file
// lock admits one daemon and is released automatically even after a crash.
func Ensure(ctx context.Context, c settings.Config, launch func(settings.Config) error) (Runtime, error) {
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	lock, err := waitGate(waitCtx, c)
	if err != nil {
		return Runtime{}, err
	}
	if e := recoverUpgrade(waitCtx, c); e != nil {
		lock.Close()
		return Runtime{}, e
	}
	lock.Close()
	if r, err := ReadRuntime(c); err == nil {
		if info, e := Probe(ctx, r); e == nil {
			r.Info = info
			return r, nil
		}
	}
	if err := launch(c); err != nil {
		return Runtime{}, err
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return Runtime{}, ctx.Err()
		case <-deadline.C:
			return Runtime{}, errors.New("daemon startup timed out; inspect ~/.keepsess/daemon.log")
		case <-tick.C:
			if r, e := ReadRuntime(c); e == nil {
				if info, e := Probe(ctx, r); e == nil {
					r.Info = info
					return r, nil
				}
			}
		}
	}
}
func Launch(c settings.Config) error {
	exe, err := selectedBinary(c)
	if err != nil {
		return err
	}
	return launchBinary(c, exe, nil)
}
func launchBinary(c settings.Config, exe string, extra []string) error {
	_, e := launchProcess(c, exe, extra)
	return e
}
func launchProcess(c settings.Config, exe string, extra []string) (*os.Process, error) {
	if err := os.MkdirAll(c.RuntimeDir(), 0700); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(filepath.Join(c.RuntimeDir(), "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer null.Close()
	cmd := exec.Command(exe, "daemon", "run")
	cmd.Env = append(os.Environ(), "KEEPSESS_UPGRADE_TOKEN=", "KEEPSESS_SETTINGS_HOME="+c.Home)
	cmd.Env = append(cmd.Env, extra...)
	cmd.Stdin = null
	cmd.Stdout = log
	cmd.Stderr = log
	detachChild(cmd)
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process, nil
}

func isLocalIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if network, ok := addr.(*net.IPNet); ok && network.IP.Equal(ip) {
			return true
		}
	}
	return false
}
