package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zytyan/keepsess/internal/core"
)

// PasswordChange is private: values are returned only by the same-origin secrets POST.
type PasswordChange struct {
	Password  string    `json:"password"`
	ChangedAt time.Time `json:"changed_at"`
}
type ConnectionGroup struct {
	core.ConnectionRecord
	EditInstance         string             `json:"-"`
	EditSession          string             `json:"-"`
	CanEdit              bool               `json:"can_edit"`
	Key                  string             `json:"key"`
	PasswordHistoryCount int                `json:"password_history_count"`
	PasswordChanges      []PasswordChange   `json:"-"`
	JumpPasswordChanges  [][]PasswordChange `json:"-"`
}
type ConnectionGroupPage struct {
	Items      []ConnectionGroup `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
}

func endpointKey(protocol string, e core.ConnectionEndpoint) string {
	b, _ := json.Marshal([]any{protocol, strings.ToLower(e.Host), e.Port, e.Username})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func ConnectionKey(r core.ConnectionRecord) string { return endpointKey(r.Protocol, r.Target) }
func credentialTime(r core.ConnectionRecord) time.Time {
	if !r.CredentialsUpdated.IsZero() {
		return r.CredentialsUpdated
	}
	return r.LastConnected
}
func connectionTime(r core.ConnectionRecord) time.Time {
	if r.CredentialsUpdated.After(r.LastConnected) {
		return r.CredentialsUpdated
	}
	return r.LastConnected
}

// Immutable private snapshots preserve changes without an ever-growing single file.
// Each agent owns its session files; the daemon alone writes manual overlays.
func (s *Store) archiveConnection(r core.ConnectionRecord, account bool) error {
	dir, err := s.connectionDir(r.Instance, false)
	if err != nil {
		return err
	}
	dir = filepath.Join(dir, "versions")
	if err = os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid connection versions directory")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	h := sha256.Sum256(b)
	name := r.Session + "." + hex.EncodeToString(h[:]) + ".json"
	var delta int64
	if account {
		delta = int64(len(b))
		if existing, e := os.Stat(filepath.Join(dir, name)); e == nil {
			delta -= existing.Size()
		}
		if s.maxBytes > 0 && s.used+delta > s.maxBytes {
			return errors.New("connection storage capacity reached")
		}
	}
	if err = privateConnectionWrite(dir, name, r); err != nil {
		return err
	}
	if account {
		s.used += delta
	}
	return nil
}

func (s *Store) connectionGroups(ctx context.Context) ([]ConnectionGroup, []string, error) {
	groups := make(map[string][]core.ConnectionRecord)
	current := make(map[string]core.ConnectionRecord)
	warnings := []string{}
	warn := func() {
		if len(warnings) < 20 {
			warnings = append(warnings, "Some private connection records could not be read; connection history may be incomplete.")
		}
	}
	dirs, err := os.ReadDir(s.root)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range dirs {
		if err = ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !d.IsDir() || !validID.MatchString(d.Name()) {
			continue
		}
		var state State
		if d.Name() == s.instance {
			state = s.Status()
		} else {
			_ = privateConnectionRead(filepath.Join(s.root, d.Name(), "state.json"), &state)
		}
		if state.ConnectionError != "" {
			warn()
		}
		dir, e := s.connectionDir(d.Name(), false)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			warn()
			continue
		}
		add := func(r core.ConnectionRecord) { key := ConnectionKey(r); groups[key] = append(groups[key], r) }
		files, e := os.ReadDir(dir)
		if e != nil {
			warn()
			continue
		}
		for _, f := range files {
			if err = ctx.Err(); err != nil {
				return nil, nil, err
			}
			session := strings.TrimSuffix(f.Name(), ".json")
			if !connectionSession.MatchString(session) {
				continue
			}
			r, e := s.Connection(d.Name(), session, true)
			if e != nil {
				warn()
				continue
			}
			add(r)
			key := ConnectionKey(r)
			if old, ok := current[key]; !ok || connectionTime(r).After(connectionTime(old)) {
				current[key] = r
			}
		}
		versions := filepath.Join(dir, "versions")
		st, e := os.Lstat(versions)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			warn()
			continue
		}
		files, e = os.ReadDir(versions)
		if e != nil {
			warn()
			continue
		}
		for _, f := range files {
			if err = ctx.Err(); err != nil {
				return nil, nil, err
			}
			if !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			var r core.ConnectionRecord
			if privateConnectionRead(filepath.Join(versions, f.Name()), &r) != nil || r.Version != 1 || r.Instance != d.Name() || !connectionSession.MatchString(r.Session) {
				warn()
				continue
			}
			add(r)
		}
	}
	result := make([]ConnectionGroup, 0, len(groups))
	for key, records := range groups {
		if err = ctx.Err(); err != nil {
			return nil, nil, err
		}
		sort.SliceStable(records, func(i, j int) bool {
			a, b := connectionTime(records[i]), connectionTime(records[j])
			if !a.Equal(b) {
				return a.Before(b)
			}
			return records[i].Instance+"/"+records[i].Session < records[j].Instance+"/"+records[j].Session
		})
		latest := records[len(records)-1]
		g := ConnectionGroup{ConnectionRecord: latest, Key: key, CanEdit: latest.Protocol == "telnet" && current[key].Session != "", EditInstance: current[key].Instance, EditSession: current[key].Session, JumpPasswordChanges: make([][]PasswordChange, len(latest.SSHJumps))}
		appendChange := func(history []PasswordChange, password string, at time.Time) []PasswordChange {
			if len(history) == 0 && password == "" {
				return history
			}
			if len(history) == 0 || history[len(history)-1].Password != password {
				return append(history, PasswordChange{password, at})
			}
			return history
		}
		for _, r := range records {
			if err = ctx.Err(); err != nil {
				return nil, nil, err
			}
			if r.FirstConnected.Before(g.FirstConnected) {
				g.FirstConnected = r.FirstConnected
			}
			if r.LastConnected.After(g.LastConnected) {
				g.LastConnected = r.LastConnected
			}
			g.PasswordChanges = appendChange(g.PasswordChanges, r.Target.Password, credentialTime(r))
			for i, jump := range latest.SSHJumps {
				for _, old := range r.SSHJumps {
					if endpointKey("ssh", old) == endpointKey("ssh", jump) {
						g.JumpPasswordChanges[i] = appendChange(g.JumpPasswordChanges[i], old.Password, credentialTime(r))
						break
					}
				}
			}
		}
		if len(g.PasswordChanges) > 0 {
			g.PasswordHistoryCount = len(g.PasswordChanges) - 1
		}
		result = append(result, g)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, warnings, nil
}
func redactGroup(g *ConnectionGroup) {
	g.Target.HasPassword = g.Target.Password != ""
	g.Target.Password = ""
	// Copy before clearing: the selected record and its private values remain independent.
	g.SSHJumps = append([]core.ConnectionEndpoint(nil), g.SSHJumps...)
	for i := range g.SSHJumps {
		g.SSHJumps[i].HasPassword = g.SSHJumps[i].Password != ""
		g.SSHJumps[i].Password = ""
	}
	g.PasswordChanges = nil
	g.JumpPasswordChanges = nil
}
func (s *Store) GroupedConnections(ctx context.Context, after string, limit int) (ConnectionGroupPage, error) {
	page := ConnectionGroupPage{Items: []ConnectionGroup{}}
	if limit < 1 || limit > 100 {
		return page, errors.New("limit must be 1..100")
	}
	if after != "" {
		b, e := hex.DecodeString(after)
		if e != nil || len(b) != 32 {
			return page, errors.New("invalid connection cursor")
		}
	}
	groups, warnings, err := s.connectionGroups(ctx)
	if err != nil {
		return page, err
	}
	page.Warnings = warnings
	for _, g := range groups {
		if g.Key <= after {
			continue
		}
		if len(page.Items) == limit {
			page.NextCursor = page.Items[len(page.Items)-1].Key
			break
		}
		redactGroup(&g)
		page.Items = append(page.Items, g)
	}
	return page, nil
}
func (s *Store) ConnectionGroup(ctx context.Context, key string, secrets bool) (ConnectionGroup, error) {
	b, e := hex.DecodeString(key)
	if e != nil || len(b) != 32 {
		return ConnectionGroup{}, errors.New("invalid connection key")
	}
	groups, _, err := s.connectionGroups(ctx)
	if err != nil {
		return ConnectionGroup{}, err
	}
	for _, g := range groups {
		if g.Key == key {
			if !secrets {
				redactGroup(&g)
			}
			return g, nil
		}
	}
	return ConnectionGroup{}, errors.New("connection information was not recorded")
}
