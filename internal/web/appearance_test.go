package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zytyan/keepsess/internal/settings"
)

func TestAppearanceAPI(t *testing.T) {
	home := t.TempDir()
	_, err := settings.LoadHome(home)
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := setup(t, Options{Listen: "127.0.0.1:0", BasePath: "/console/", SettingsHome: home})
	value := settings.DefaultAppearance()
	value.Rules[0].Pattern = "hello"
	body, _ := json.Marshal(value)
	for _, tc := range []struct {
		origin, content string
		status          int
	}{{"", "application/json", 403}, {"http://evil.example", "application/json", 403}, {strings.TrimSuffix(s.Info().URL, "/console/"), "text/plain", 403}, {strings.TrimSuffix(s.Info().URL, "/console/"), "application/json", 200}} {
		req, _ := http.NewRequest("POST", s.Info().URL+"api/appearance", bytes.NewReader(body))
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", tc.content)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != tc.status {
			t.Fatalf("%+v: %d %s", tc, res.StatusCode, b)
		}
	}
	cfg, err := settings.LoadHome(home)
	if err != nil || cfg.Web.Appearance.Rules[0].Pattern != "hello" {
		t.Fatal(cfg.Web.Appearance, err)
	}
}
