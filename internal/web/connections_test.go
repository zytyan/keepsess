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
	"github.com/zytyan/keepsess/internal/history"
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

func TestGroupedConnectionManagementAPI(t *testing.T) {
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0", BasePath: "/nested/"})
	now := time.Now().UTC()
	r := core.ConnectionRecord{Version: 1, Instance: m.InstanceID(), Session: "s1", Protocol: "ssh", Mode: "exec", FirstConnected: now, LastConnected: now, Target: core.ConnectionEndpoint{Host: "group.example", Port: 22, Username: "operator", Password: "old-group-secret"}}
	if err := st.RecordConnection(r); err != nil {
		t.Fatal(err)
	}
	r.Target.Password = "new-group-secret"
	r.LastConnected = now.Add(time.Second)
	if err := st.RecordConnection(r); err != nil {
		t.Fatal(err)
	}
	st.Emit(core.Event{Kind: "session", Session: "s1"})
	deadline := time.Now().Add(3 * time.Second)
	for st.Status().WrittenSeq < st.Status().LastSeq && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	response, err := http.Get(s.Info().URL + "api/connections")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(body), "group-secret") {
		t.Fatal("secret leaked in grouped list")
	}
	var page struct {
		Items []struct {
			Key   string `json:"key"`
			Count int    `json:"password_history_count"`
		} `json:"items"`
	}
	if err = json.Unmarshal(body, &page); err != nil || len(page.Items) != 1 || page.Items[0].Count != 1 {
		t.Fatal("invalid grouped list", err)
	}
	key := page.Items[0].Key
	response, err = http.Get(s.Info().URL + "api/connection?key=" + key)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || strings.Contains(string(body), "group-secret") {
		t.Fatal("metadata leak")
	}
	originURL, _ := url.Parse(s.Info().URL)
	req, _ := http.NewRequest("POST", s.Info().URL+"api/connection-secrets", strings.NewReader(`{"key":"`+key+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", originURL.Scheme+"://"+originURL.Host)
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(body), `"password":"new-group-secret"`) || !strings.Contains(string(body), `"password":"old-group-secret"`) || !strings.Contains(string(body), `"changed_at"`) {
		t.Fatal("password history unavailable")
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable password history")
	}
}

func TestGroupedManualSavePreservesDisplayedPassword(t *testing.T) {
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0", BasePath: "/nested/"})
	now := time.Now().UTC().Add(-time.Minute)
	r := core.ConnectionRecord{Version: 1, Instance: m.InstanceID(), Session: "s1", Protocol: "telnet", Mode: "stream", FirstConnected: now, LastConnected: now, Target: core.ConnectionEndpoint{Host: "manual.example", Port: 23, Username: "user", Password: "older-value"}}
	if err := st.RecordConnection(r); err != nil {
		t.Fatal(err)
	}
	r.Session = "s2"
	r.Target.Password = "displayed-value"
	r.LastConnected = now.Add(time.Second)
	if err := st.RecordConnection(r); err != nil {
		t.Fatal(err)
	}
	st.Emit(core.Event{Kind: "session", Session: "s2"})
	deadline := time.Now().Add(3 * time.Second)
	for st.Status().WrittenSeq < st.Status().LastSeq && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// Retire the newer record under another username, retaining its old account snapshot.
	if err := st.SaveConnectionCredentials(m.InstanceID(), "s2", "renamed", nil); err != nil {
		t.Fatal(err)
	}
	key := history.ConnectionKey(r)
	origin, _ := url.Parse(s.Info().URL)
	req, _ := http.NewRequest("POST", s.Info().URL+"api/connection-credentials", strings.NewReader(`{"key":"`+key+`","username":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin.Scheme+"://"+origin.Host)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("save failed", response.StatusCode)
	}
	saved, err := st.Connection(m.InstanceID(), "s1", true)
	if err != nil || saved.Target.Password != "displayed-value" {
		t.Fatal("blank input reverted the displayed aggregate password", err)
	}
}
