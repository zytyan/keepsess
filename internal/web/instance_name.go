package web

import (
	"encoding/json"
	"io"
	"net/http"
)

func (s *Server) instanceName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var args struct {
		Instance  string  `json:"instance"`
		HumanName *string `json:"human_name"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	d.DisallowUnknownFields()
	if err := d.Decode(&args); err != nil {
		fail(w, err)
		return
	}
	if d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid trailing JSON", 400)
		return
	}
	if args.HumanName == nil {
		http.Error(w, "human_name is required; use an empty string to clear", 400)
		return
	}
	if err := s.Store.RenameHumanInstance(args.Instance, *args.HumanName); err != nil {
		fail(w, err)
		return
	}
	respond(w, map[string]string{"instance": args.Instance, "human_name": *args.HumanName})
}
