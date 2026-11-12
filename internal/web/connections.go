package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/zytyan/keepsess/internal/history"
)

func (s *Server) connections(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "invalid limit", 400)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	page, err := s.Store.GroupedConnections(ctx, r.URL.Query().Get("after"), limit)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, page)
}

func (s *Server) connection(w http.ResponseWriter, r *http.Request) {
	if key := r.URL.Query().Get("key"); key != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		record, err := s.Store.ConnectionGroup(ctx, key, false)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, record)
		return
	}
	record, err := s.Store.Connection(r.URL.Query().Get("instance"), r.URL.Query().Get("session"), false)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, record)
}

func (s *Server) connectionSecrets(w http.ResponseWriter, r *http.Request) {
	s.connectionPost(w, r, false)
}
func (s *Server) connectionCredentials(w http.ResponseWriter, r *http.Request) {
	s.connectionPost(w, r, true)
}
func (s *Server) connectionPost(w http.ResponseWriter, r *http.Request, save bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST required", 405)
		return
	}
	var args struct {
		Key      string  `json:"key"`
		Instance string  `json:"instance"`
		Session  string  `json:"session"`
		Username *string `json:"username"`
		Password *string `json:"password"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(&args); err != nil {
		http.Error(w, "invalid connection request", 400)
		return
	}
	if d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid trailing JSON", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if args.Key != "" {
		if args.Instance != "" || args.Session != "" {
			http.Error(w, "key and instance/session are mutually exclusive", 400)
			return
		}
		group, err := s.Store.ConnectionGroup(ctx, args.Key, save)
		if err != nil {
			fail(w, err)
			return
		}
		if save && !group.CanEdit {
			http.Error(w, "historical credentials are read-only", 400)
			return
		}
		args.Instance, args.Session = group.Instance, group.Session
		if save {
			args.Instance, args.Session = group.EditInstance, group.EditSession
			if args.Password == nil {
				password := group.Target.Password
				args.Password = &password
			}
		}
	}
	if save {
		if args.Username == nil {
			http.Error(w, "username is required; omit password to preserve it or use an empty string to clear", 400)
			return
		}
		if err := s.Store.SaveConnectionCredentials(args.Instance, args.Session, *args.Username, args.Password); err != nil {
			fail(w, err)
			return
		}
		record, err := s.Store.Connection(args.Instance, args.Session, false)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, map[string]any{"saved": true, "key": history.ConnectionKey(record)})
		return
	}
	if args.Username != nil || args.Password != nil {
		http.Error(w, "credentials are not accepted by this endpoint", 400)
		return
	}
	if args.Key != "" {
		record, err := s.Store.ConnectionGroup(ctx, args.Key, true)
		if err != nil {
			fail(w, err)
			return
		}
		jumps := make([]string, len(record.SSHJumps))
		for i, j := range record.SSHJumps {
			jumps[i] = j.Password
		}
		respond(w, struct {
			Password            string                     `json:"password"`
			SSHJumps            []string                   `json:"ssh_jumps"`
			PasswordChanges     []history.PasswordChange   `json:"password_changes"`
			JumpPasswordChanges [][]history.PasswordChange `json:"jump_password_changes"`
		}{record.Target.Password, jumps, record.PasswordChanges, record.JumpPasswordChanges})
		return
	}
	record, err := s.Store.Connection(args.Instance, args.Session, true)
	if err != nil {
		fail(w, err)
		return
	}
	// Return only the requested passwords, not another copy of the full private record.
	jumps := make([]string, len(record.SSHJumps))
	for i, j := range record.SSHJumps {
		jumps[i] = j.Password
	}
	respond(w, struct {
		Password string   `json:"password"`
		SSHJumps []string `json:"ssh_jumps"`
	}{record.Target.Password, jumps})
}
