package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zytyan/keepsess/internal/history"
)

func TestHumanInstanceNameAPI(t *testing.T) {
	s, m, _ := setup(t, Options{Listen: "127.0.0.1:0", BasePath: "/console/"})
	connectSSH(t, m, "exec")
	origin := strings.TrimSuffix(s.Info().URL, "/console/")
	body := `{"instance":"` + m.InstanceID() + `","human_name":"人类名称"}`
	for _, tc := range []struct {
		body, origin, content string
		code                  int
	}{
		{body, "", "application/json", 403},
		{body, "http://evil.example", "application/json", 403},
		{body, origin, "text/plain", 403},
		{`{"instance":"../bad","human_name":"x"}`, origin, "application/json", 400},
		{`{"instance":"missing","human_name":"x"}`, origin, "application/json", 400},
		{`{"instance":"` + m.InstanceID() + `"}`, origin, "application/json", 400},
		{`{"instance":"` + m.InstanceID() + `","human_name":null}`, origin, "application/json", 400},
		{body + `{}`, origin, "application/json", 400},
		{strings.TrimSuffix(body, "}") + `,"other":true}`, origin, "application/json", 400},
		{strings.Replace(body, "人类名称", " name", 1), origin, "application/json", 400},
		{body, origin, "application/json", 200},
	} {
		req, _ := http.NewRequest("POST", s.Info().URL+"api/instance-name", strings.NewReader(tc.body))
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", tc.content)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != tc.code {
			t.Fatalf("%+v: %d %s", tc, res.StatusCode, b)
		}
	}
	res, err := http.Get(s.Info().URL + "api/instances")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var list []history.Instance
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].HumanName != "人类名称" || m.InstanceName() != "" {
		t.Fatal(list, m.InstanceName())
	}
}
