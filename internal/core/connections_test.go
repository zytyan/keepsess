package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zytyan/keepsess/internal/testutil"
)

type connectionRecorder struct {
	eventRecorder
	records []ConnectionRecord
	reject  bool
}

func (r *connectionRecorder) RecordConnection(c ConnectionRecord) error {
	if r.reject {
		return errors.New("private sink failure")
	}
	r.records = append(r.records, c)
	return nil
}
func TestPrivateConnectionRecordsSuccessReconnectAndFailures(t *testing.T) {
	for _, protocol := range []string{"ssh", "telnet"} {
		t.Run(protocol, func(t *testing.T) {
			target := ""
			if protocol == "ssh" {
				target, _ = testutil.SSH(t)
			} else {
				target = testutil.TelnetSwitch(t)
			}
			jump, key, _ := testutil.SSHJump(t, target, false)
			near, nearKey, _ := testutil.SSHJump(t, jump, false)
			m := manager(t)
			r := &connectionRecorder{}
			m.SetEventSink(r)
			o := options(target, protocol, "stream")
			o.Name = "original"
			o.Password = "target-private"
			o.KeyPassphrase = "never-persist-passphrase"
			o.SSHJumps = []SSHJump{jumpOptions(near, nearKey), jumpOptions(jump, key)}
			o.SSHJumps[0].KeyPassphrase = "never-persist-jump-passphrase"
			id := connect(t, m, o)
			if len(r.records) != 1 {
				t.Fatal("missing successful connection")
			}
			first := r.records[0]
			if first.Instance != m.InstanceID() || first.Session != id || first.Target.Password != "target-private" || len(first.SSHJumps) != 2 || first.SSHJumps[0].Host != o.SSHJumps[0].Host || first.SSHJumps[1].Port != o.SSHJumps[1].Port || first.SSHJumps[0].Password != "jump-secret" || first.SSHJumps[1].Password != "jump-secret" {
				t.Fatal("incorrect private connection metadata")
			}
			b, _ := json.Marshal(first)
			if strings.Contains(string(b), "passphrase") {
				t.Fatal("passphrase persisted")
			}
			_, _ = m.RenameSession(id, "renamed")
			v, err := m.Reconnect(context.Background(), id)
			if err != nil || !v.Connected || len(r.records) != 2 || r.records[1].Session != id || r.records[1].Name != "renamed" {
				t.Fatal(v, err)
			}
			// A successful dial invalidated by close_all is covered by existing cancellation tests;
			// rejected/failed connects must not create records either.
			bad := o
			bad.Host = ""
			if _, err = m.Connect(context.Background(), bad); err == nil || len(r.records) != 2 {
				t.Fatal("failed connection recorded")
			}
			bad = o
			bad.Name = "failed"
			bad.SSHJumps = append([]SSHJump(nil), o.SSHJumps...)
			bad.SSHJumps[0].Password = "invalid-password"
			if _, err = m.Connect(context.Background(), bad); err == nil || len(r.records) != 2 {
				t.Fatal("failed SSH authentication recorded")
			}
			s, _ := m.Get(id)
			s.mu.Lock()
			s.options.SSHJumps[0].Password = "wrong-password"
			s.mu.Unlock()
			if v, err = m.Reconnect(context.Background(), id); err != nil || v.Connected || len(r.records) != 2 {
				t.Fatal("failed reconnect recorded")
			}
			b, _ = json.Marshal(r.snapshot())
			if strings.Contains(string(b), "target-private") || strings.Contains(string(b), "jump-secret") {
				t.Fatal("private metadata in public events")
			}
		})
	}
}
func TestConnectionSinkFailureDoesNotFailRemoteConnect(t *testing.T) {
	addr, _ := testutil.SSH(t)
	m := manager(t)
	r := &connectionRecorder{reject: true}
	m.SetEventSink(r)
	v, err := m.Connect(context.Background(), options(addr, "ssh", "exec"))
	if err != nil || !v.Connected || v.Warning == "" {
		t.Fatal(v, err)
	}
}
