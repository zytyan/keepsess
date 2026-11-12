package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zytyan/keepsess/internal/core"
)

const connectionLimit = 256 << 10

var connectionSession = regexp.MustCompile(`^s[1-9][0-9]*$`)

// ConnectionPage never includes password values.
type ConnectionPage struct {
	Items      []core.ConnectionRecord `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
	Warnings   []string                `json:"warnings,omitempty"`
}
type manualCredentials struct {
	Updated  time.Time `json:"updated,omitempty"`
	Version  int       `json:"version"`
	Username string    `json:"username"`
	Password string    `json:"password"`
}

// RecordConnection shares the journal's bounded writer, but not its public events,
// projections, sequence numbers or subscribers. No disk work happens on this path.
func (s *Store) RecordConnection(r core.ConnectionRecord) error {
	b, err := json.Marshal(r)
	if err != nil || len(b) > connectionLimit {
		s.mu.Lock()
		s.state.ConnectionError = "Connection information exceeds storage limit and was not saved."
		s.mu.Unlock()
		return errors.New("connection information exceeds storage limit")
	}
	if r.Instance != s.instance || !connectionSession.MatchString(r.Session) {
		return errors.New("invalid connection identity")
	}
	r.SSHJumps = append([]core.ConnectionEndpoint(nil), r.SSHJumps...)
	s.mu.Lock()
	defer s.mu.Unlock()
	reject := func() error {
		s.state.ConnectionError = "Connection information was not queued for saving; storage closed or queue full."
		return errors.New(s.state.ConnectionError)
	}
	if s.closed || s.view || s.queuedBytes+len(b) > queueBytes {
		return reject()
	}
	select {
	case s.queue <- queued{size: len(b), connection: &r}:
		s.queuedBytes += len(b)
		s.connectionPending++
		s.hasSessions = true
		return nil
	default:
		return reject()
	}
}

func (s *Store) connectionDir(instance string, create bool) (string, error) {
	dir, err := s.instanceDir(instance)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("unknown connection instance")
	}
	dir = filepath.Join(dir, "connections")
	if create {
		if err = os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	info, err = os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid connection storage directory")
	}
	return dir, nil
}

func privateConnectionRead(path string, v any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("invalid connection storage file")
	}
	b, err := readSmall(path, connectionLimit)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func privateConnectionWrite(dir, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > connectionLimit {
		return errors.New("connection information exceeds storage limit")
	}
	f, err := os.CreateTemp(dir, ".connection-*") // 0600; unique across browser requests and agents.
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), filepath.Join(dir, name))
}

func (s *Store) saveConnection(r core.ConnectionRecord) error {
	dir, err := s.connectionDir(r.Instance, true)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, r.Session+".json")
	r.CredentialsUpdated = r.LastConnected
	var old core.ConnectionRecord
	if err = privateConnectionRead(path, &old); err == nil {
		if old.Version != 1 || old.Instance != r.Instance || old.Session != r.Session {
			return errors.New("invalid existing connection")
		}
		r.FirstConnected = old.FirstConnected
		if !reflect.DeepEqual(old.Target, r.Target) || !reflect.DeepEqual(old.SSHJumps, r.SSHJumps) {
			if err = s.archiveConnection(old, true); err != nil {
				return err
			}
		} else {
			r.CredentialsUpdated = old.CredentialsUpdated
			if r.CredentialsUpdated.IsZero() {
				r.CredentialsUpdated = old.LastConnected
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	var previous int64
	if st, e := os.Stat(path); e == nil {
		previous = st.Size()
	}
	delta := int64(len(b)) - previous
	if s.maxBytes > 0 && s.used+delta > s.maxBytes {
		return errors.New("connection storage capacity reached")
	}
	if err = privateConnectionWrite(dir, r.Session+".json", r); err != nil {
		return err
	}
	s.used += delta
	if !s.metaHasSessions {
		path := filepath.Join(s.root, s.instance, "instance.json")
		var meta Instance
		if err = privateConnectionRead(path, &meta); err != nil {
			return err
		}
		present := true
		meta.HasSessions = &present
		if err = writeJSON(path, meta); err != nil {
			return err
		}
		s.metaHasSessions = true
	}
	return nil
}

func (s *Store) Connection(instance, session string, secrets bool) (core.ConnectionRecord, error) {
	var r core.ConnectionRecord
	if !connectionSession.MatchString(session) {
		return r, errors.New("invalid connection session")
	}
	dir, err := s.connectionDir(instance, false)
	if errors.Is(err, os.ErrNotExist) {
		return r, errors.New("connection information was not recorded")
	}
	if err != nil {
		return r, err
	}
	if err = privateConnectionRead(filepath.Join(dir, session+".json"), &r); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return r, errors.New("connection information was not recorded")
		}
		return core.ConnectionRecord{}, errors.New("connection information could not be read")
	}
	if r.Version != 1 || r.Instance != instance || r.Session != session {
		return core.ConnectionRecord{}, errors.New("unsupported or invalid connection record")
	}
	if r.Protocol == "telnet" {
		var manual manualCredentials
		if err = privateConnectionRead(filepath.Join(dir, session+".credentials.json"), &manual); err == nil {
			if manual.Version != 1 {
				return core.ConnectionRecord{}, errors.New("unsupported saved credentials")
			}
			r.Target.Username, r.Target.Password, r.Manual = manual.Username, manual.Password, true
			r.CredentialsUpdated = manual.Updated
		} else if !errors.Is(err, os.ErrNotExist) {
			return core.ConnectionRecord{}, errors.New("saved credentials could not be read")
		}
	}
	// Session aliases can change after connection; prefer the latest available projection.
	var meta Record
	metaDir := filepath.Join(s.root, instance)
	if privateConnectionRead(filepath.Join(metaDir, session+".meta"), &meta) == nil && meta.Event.Session == session {
		r.Name = meta.Event.Name
	}
	r.Target.HasPassword = r.Target.Password != ""
	if !secrets {
		r.Target.Password = ""
	}
	for i := range r.SSHJumps {
		r.SSHJumps[i].HasPassword = r.SSHJumps[i].Password != ""
		if !secrets {
			r.SSHJumps[i].Password = ""
		}
	}
	return r, nil
}

// Browser edits have their own file: the owning agent never overwrites them on reconnect.
func (s *Store) SaveConnectionCredentials(instance, session, username string, password *string) error {
	s.connectionEditMu.Lock()
	defer s.connectionEditMu.Unlock()
	if !utf8.ValidString(username) || len(username) > 4096 || (password != nil && (!utf8.ValidString(*password) || len(*password) > 65536)) {
		return errors.New("invalid credentials or credentials too long")
	}
	r, err := s.Connection(instance, session, true)
	if err != nil {
		return err
	}
	if r.Protocol != "telnet" {
		return errors.New("manual credentials are only supported for Telnet")
	}
	dir, err := s.connectionDir(instance, false)
	if err != nil {
		return err
	}
	savedPassword := r.Target.Password
	if password != nil {
		savedPassword = *password
	}
	if r.Target.Username != username || r.Target.Password != savedPassword {
		if err = s.archiveConnection(r, false); err != nil {
			return errors.New("previous credentials could not be preserved")
		}
	}
	updated := r.CredentialsUpdated
	if r.Target.Username != username || r.Target.Password != savedPassword {
		updated = time.Now().UTC()
	}
	if err = privateConnectionWrite(dir, session+".credentials.json", manualCredentials{Updated: updated, Version: 1, Username: username, Password: savedPassword}); err != nil {
		return errors.New("credentials could not be saved; check storage permissions and availability")
	}
	return nil
}

func (s *Store) Connections(ctx context.Context, after string, limit int) (ConnectionPage, error) {
	page := ConnectionPage{Items: []core.ConnectionRecord{}}
	warn := func(message string) {
		if len(page.Warnings) < 20 {
			page.Warnings = append(page.Warnings, message)
		}
	}
	if limit < 1 || limit > 100 {
		return page, errors.New("limit must be 1..100")
	}
	if after != "" {
		parts := strings.Split(after, "/")
		if len(parts) != 2 || !validID.MatchString(parts[0]) || !connectionSession.MatchString(parts[1]) {
			return page, errors.New("invalid connection cursor")
		}
	}
	dirs, err := os.ReadDir(s.root)
	if err != nil {
		return page, err
	}
	// ReadDir sorts names; the stable cursor remains valid when new records appear.
	for _, d := range dirs {
		if err = ctx.Err(); err != nil {
			return page, err
		}
		if !d.IsDir() || !validID.MatchString(d.Name()) {
			continue
		}
		instance := d.Name()
		var state State
		if instance == s.instance {
			state = s.Status()
		} else {
			_ = privateConnectionRead(filepath.Join(s.root, instance, "state.json"), &state)
		}
		if state.ConnectionError != "" {
			warn(fmt.Sprintf("%s: %s", instance, state.ConnectionError))
		}
		dir, e := s.connectionDir(instance, false)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			warn(instance + ": connection storage could not be read")
			continue
		}
		files, e := os.ReadDir(dir)
		if e != nil {
			warn(instance + ": connection storage could not be read")
			continue
		}
		for _, f := range files {
			if err = ctx.Err(); err != nil {
				return page, err
			}
			session := strings.TrimSuffix(f.Name(), ".json")
			if !connectionSession.MatchString(session) || instance+"/"+session <= after {
				continue
			}
			if len(page.Items) == limit {
				last := page.Items[len(page.Items)-1]
				page.NextCursor = last.Instance + "/" + last.Session
				return page, nil
			}
			r, e := s.Connection(instance, session, false)
			if e != nil {
				warn(instance + "/" + session + ": connection information could not be read")
				continue
			}
			page.Items = append(page.Items, r)
		}
	}
	return page, nil
}
