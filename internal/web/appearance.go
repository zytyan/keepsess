package web

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/zytyan/keepsess/internal/settings"
)

func (s *Server) appearance(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		if s.settingsHome == "" {
			http.Error(w, "此 Web 实例未启用配置保存", 403)
			return
		}
		var a settings.Appearance
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 48<<10))
		d.DisallowUnknownFields()
		if err := d.Decode(&a); err != nil {
			http.Error(w, "invalid appearance JSON", 400)
			return
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			http.Error(w, "invalid trailing JSON", 400)
			return
		}
		if err := settings.SaveAppearance(s.settingsHome, a); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	a := settings.DefaultAppearance()
	if s.settingsHome != "" {
		cfg, err := settings.LoadHome(s.settingsHome)
		if err != nil {
			http.Error(w, "无法读取显示配置，请检查 ~/.keepsess.toml", 500)
			return
		}
		a = cfg.Web.Appearance
	}
	respond(w, map[string]any{"appearance": a, "defaults": settings.DefaultAppearance(), "writable": s.settingsHome != ""})
}
