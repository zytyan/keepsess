package web

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/testutil"
)

func TestConnectionManagementAPI(t *testing.T) {
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0", BasePath: "/nested/"})
	sshID := connectSSH(t, m, "exec")
	addr := testutil.TelnetSwitch(t)
	host, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	v, err := m.Connect(context.Background(), core.Options{Host: host, Port: n, Protocol: "telnet", Username: "original", Password: "original-secret"})
	if err != nil {
		t.Fatal(err)
	}
	telnetID := v.Session
	// A subsequent journal event is a barrier for the private queue as well.
	deadline := time.Now().Add(3 * time.Second)
	for st.Status().WrittenSeq < st.Status().LastSeq && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	base := s.Info().URL
	parsed, _ := url.Parse(base)
	origin := parsed.Scheme + "://" + parsed.Host
	request := func(method, path, body, requestOrigin string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, base+"api/"+path, strings.NewReader(body))
		if method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		if requestOrigin != "" {
			req.Header.Set("Origin", requestOrigin)
		}
		response, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		b, _ := io.ReadAll(response.Body)
		if response.Header.Get("Cache-Control") != "no-store" && response.StatusCode == 200 {
			t.Fatal("cacheable credentials endpoint")
		}
		return response.StatusCode, string(b)
	}
	identity := `{"instance":"` + m.InstanceID() + `","session":"` + sshID + `"}`
	code, body := request("GET", "connections?limit=1", "", "")
	if code != 200 || !strings.Contains(body, `"next_cursor"`) || strings.Contains(body, "secret") {
		t.Fatal(code, body)
	}
	code, body = request("GET", "connection?instance="+m.InstanceID()+"&session="+sshID, "", "")
	if code != 200 || strings.Contains(body, "secret") || !strings.Contains(body, `"has_password":true`) {
		t.Fatal(code, body)
	}
	for _, badOrigin := range []string{"", "http://evil.invalid"} {
		code, _ = request("POST", "connection-secrets", identity, badOrigin)
		if code != 403 {
			t.Fatal("secret origin check", code)
		}
	}
	code, body = request("POST", "connection-secrets", identity, origin)
	if code != 200 || !strings.Contains(body, `"password":"secret"`) {
		t.Fatal(code, body)
	}
	code, _ = request("GET", "connection-secrets?instance="+m.InstanceID()+"&session="+sshID, "", "")
	if code != 405 {
		t.Fatal("GET returned secrets")
	}
	code, _ = request("POST", "connection-credentials", strings.TrimSuffix(identity, "}")+`,"username":"edited","password":"edited"}`, origin)
	if code != 400 {
		t.Fatal("SSH edit allowed")
	}
	edit := `{"instance":"` + m.InstanceID() + `","session":"` + telnetID + `","username":"human","password":"human-secret"}`
	code, body = request("POST", "connection-credentials", edit, origin)
	if code != 200 || strings.Contains(body, "secret") {
		t.Fatal(code, body)
	}
	// Reconnect still uses the original runtime options; it must preserve the manual overlay.
	if _, err = m.Reconnect(context.Background(), telnetID); err != nil {
		t.Fatal(err)
	}
	for st.Status().WrittenSeq < st.Status().LastSeq && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	saved, err := st.Connection(m.InstanceID(), telnetID, true)
	if err != nil || !saved.Manual || saved.Target.Password != "human-secret" {
		t.Fatal("manual credentials lost", err)
	}
	id := `{"instance":"` + m.InstanceID() + `","session":"` + telnetID + `"}`
	code, _ = request("POST", "connection-credentials", id, origin)
	if code != 400 {
		t.Fatal("implicit clearing accepted")
	}
	code, _ = request("POST", "connection-secrets", id+" {}", origin)
	if code != 400 {
		t.Fatal("trailing JSON accepted")
	}
	code, _ = request("POST", "connection-secrets", strings.TrimSuffix(id, "}")+`,"extra":true}`, origin)
	if code != 400 {
		t.Fatal("unknown field accepted")
	}
	_, _ = m.Close(sshID)
	code, body = request("POST", "connection-secrets", identity, origin)
	if code != 200 || !json.Valid([]byte(body)) {
		t.Fatal("closed record unavailable", code)
	}
}
