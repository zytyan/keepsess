package history

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/core"
)

func TestConnectionGroupsRotationAndIsolation(t *testing.T) {
	root := t.TempDir()
	base := time.Now().UTC().Add(-time.Hour)
	record := func(instance, session, password string, minute int) core.ConnectionRecord {
		r := connectionFixture(instance, session, "ssh")
		r.FirstConnected = base.Add(time.Duration(minute) * time.Minute)
		r.LastConnected = r.FirstConnected
		r.Target.Password = password
		return r
	}
	save := func(instance string, records ...core.ConnectionRecord) {
		t.Helper()
		s, err := Open(root, instance, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range records {
			if err = s.RecordConnection(r); err != nil {
				t.Fatal(err)
			}
		}
		s.Close()
		if s.Status().ConnectionError != "" {
			t.Fatal(s.Status().ConnectionError)
		}
	}
	a := record("old", "s1", "first-value", 0)
	b := record("old", "s1", "second-value", 1)
	b.SSHJumps[0].Password = "new-jump-value"
	c := b
	c.FirstConnected = base.Add(2 * time.Minute)
	c.LastConnected = c.FirstConnected
	save("old", a, b, c)
	d := record("live", "s1", "third-value", 3)
	d.Target.Host = "LOCALHOST"
	otherUser := record("live", "s2", "other-user-value", 4)
	otherUser.Target.Username = "other"
	otherPort := record("live", "s3", "other-port-value", 4)
	otherPort.Target.Port = 2222
	otherProtocol := record("live", "s4", "other-protocol-value", 4)
	otherProtocol.Protocol = "telnet"
	save("live", d, otherUser, otherPort, otherProtocol)
	v, err := View(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	page, err := v.GroupedConnections(context.Background(), "", 100)
	if err != nil || len(page.Items) != 4 {
		t.Fatal("group count", len(page.Items), err)
	}
	encoded, _ := json.Marshal(page)
	for _, secret := range []string{"first-value", "second-value", "third-value", "jump-secret", "new-jump-value"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("secret in metadata")
		}
	}
	key := ConnectionKey(a)
	g, err := v.ConnectionGroup(context.Background(), key, true)
	if err != nil {
		t.Fatal(err)
	}
	if g.Target.Password != "third-value" || g.PasswordHistoryCount != 2 || len(g.PasswordChanges) != 3 || g.PasswordChanges[0].Password != "first-value" || g.PasswordChanges[1].Password != "second-value" || !g.FirstConnected.Equal(base) || !g.LastConnected.Equal(d.LastConnected) {
		t.Fatal("rotation history incorrect")
	}
	if !g.PasswordChanges[1].ChangedAt.Equal(b.LastConnected) {
		t.Fatal("unchanged reconnect moved password timestamp")
	}
	// Persisted snapshots survive reopening, and have private permissions.
	files, err := os.ReadDir(filepath.Join(root, "old", "connections", "versions"))
	if err != nil || len(files) != 1 {
		t.Fatal("unexpected snapshots", len(files), err)
	}
	st, err := os.Stat(filepath.Join(root, "old", "connections", "versions", files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
		t.Fatal("non-private snapshot")
	}
	seen := map[string]bool{}
	after := ""
	for {
		p, e := v.GroupedConnections(context.Background(), after, 1)
		if e != nil || len(p.Items) != 1 {
			t.Fatal("pagination", e)
		}
		if seen[p.Items[0].Key] {
			t.Fatal("duplicate group")
		}
		seen[p.Items[0].Key] = true
		if p.NextCursor == "" {
			break
		}
		after = p.NextCursor
	}
	if len(seen) != 4 {
		t.Fatal("pagination lost groups")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = v.GroupedConnections(ctx, "", 50); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err = v.GroupedConnections(context.Background(), "bad", 50); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}

func TestConnectionGroupsManualChangesAndClear(t *testing.T) {
	s, err := Open(t.TempDir(), "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	r := connectionFixture("manual", "s1", "telnet")
	if err = s.RecordConnection(r); err != nil {
		t.Fatal(err)
	}
	s.Close()
	v, err := View(s.root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	key := ConnectionKey(r)
	for _, p := range []string{"manual-one", "manual-two", "manual-two", ""} {
		if err = v.SaveConnectionCredentials("manual", "s1", "user", savedPassword(p)); err != nil {
			t.Fatal(err)
		}
	}
	g, err := v.ConnectionGroup(context.Background(), key, true)
	if err != nil || g.Target.Password != "" || g.PasswordHistoryCount != 3 || len(g.PasswordChanges) != 4 || !g.CanEdit {
		t.Fatal("manual history incorrect", err)
	}
	if g.PasswordChanges[2].Password != "manual-two" || g.PasswordChanges[3].Password != "" {
		t.Fatal("clear not recorded")
	}
	if err = v.SaveConnectionCredentials("manual", "s1", "new-user", savedPassword("new-user-value")); err != nil {
		t.Fatal(err)
	}
	old, err := v.ConnectionGroup(context.Background(), key, true)
	if err != nil || old.CanEdit || old.Target.Username != "user" {
		t.Fatal("old identity was lost or editable", err)
	}
	page, err := v.GroupedConnections(context.Background(), "", 50)
	if err != nil || len(page.Items) != 2 {
		t.Fatal("identity grouping failed", err)
	}
	for _, item := range page.Items {
		if len(item.PasswordChanges) != 0 || item.Target.Password != "" {
			t.Fatal("redaction failed")
		}
	}
}
