package core

import (
	"bytes"
	"encoding/json"
	"golang.org/x/crypto/ssh"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/zytyan/keepsess/internal/testutil"
)

type eventRecorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *eventRecorder) Emit(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}
func (r *eventRecorder) snapshot() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}
func TestEventsObserveWithoutConsumingAndDoNotExposeAuth(t *testing.T) {
	addr, _ := testutil.SSHWithExec(t, func(_ string, ch ssh.Channel, _ <-chan *ssh.Request) {
		_, _ = io.WriteString(ch, "stdout\n")
		_, _ = io.WriteString(ch.Stderr(), "stderr\n")
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
	})
	m := manager(t)
	r := &eventRecorder{}
	m.SetEventSink(r)
	o := options(addr, "ssh", "exec")
	o.Password = "authentication-secret"
	o.Name = "prod"
	id := connect(t, m, o)
	v := run(t, m, "exec", Request{Session: id, Command: "both", Env: map[string]string{"SECRET": "environment-secret"}})
	if v.ExitCode == nil || *v.ExitCode != 0 || !strings.Contains(v.Output, "stdout") || !strings.Contains(v.Output, "stderr") {
		t.Fatal(v)
	}
	tasks, err := m.TaskList(id)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	again := run(t, m, "read_history", Request{Handle: tasks[0].Handle})
	if again.Output != v.Output {
		t.Fatal(again, v)
	}
	_, _ = m.RenameSession(id, "renamed")
	_, _ = m.Close(id)
	var output bytes.Buffer
	closed := false
	command := false
	for _, e := range r.snapshot() {
		if e.Kind == "output" {
			output.Write(e.Data)
		}
		if e.Command == "both" {
			command = true
		}
		if e.Session == id && e.Handle == "" && e.Disposed && e.Name == "renamed" {
			closed = true
		}
	}
	b, _ := json.Marshal(r.snapshot())
	if bytes.Contains(b, []byte("authentication-secret")) || bytes.Contains(b, []byte("environment-secret")) {
		t.Fatal("credential metadata in events")
	}
	if !closed || !command || output.String() != v.Output {
		t.Fatal(closed, command, output.String(), v.Output)
	}
}
func TestCloseDisconnectedSessionStillEmitsTombstone(t *testing.T) {
	addr, _ := testutil.SSH(t)
	m := manager(t)
	r := &eventRecorder{}
	m.SetEventSink(r)
	id := connect(t, m, options(addr, "ssh", "exec"))
	s, _ := m.Get(id)
	s.disconnect("test disconnect")
	_, _ = m.Close(id)
	events := r.snapshot()
	if !events[len(events)-1].Disposed {
		t.Fatal("missing closed tombstone", events)
	}
}
func TestInputEventsUseOverrideEncodingAndPreserveBytes(t *testing.T) {
	addr := inputServer(t)
	m := manager(t)
	r := &eventRecorder{}
	m.SetEventSink(r)
	id := connect(t, m, options(addr, "ssh", "exec"))
	task := run(t, m, "exec", Request{Session: id, Command: "echo", Interactive: true, CollectOutput: noCollect()})
	v := run(t, m, "send_text", Request{Handle: task.Handle, Data: "中文", CollectOutput: noCollect(), Config: cfg(map[string]any{"encoding": "gbk"})})
	if v.InputState != "sent" {
		t.Fatal(v)
	}
	found := false
	for _, e := range r.snapshot() {
		if e.Kind == "input" {
			found = true
			if e.Encoding != "gbk" || !bytes.Equal(e.Data, []byte{0xd6, 0xd0, 0xce, 0xc4}) || e.Receipt.WrittenBytes != 4 {
				t.Fatal(e)
			}
		}
	}
	if !found {
		t.Fatal("missing input event")
	}
}

func TestTransportFailureEventMarksTaskUnknown(t *testing.T) {
	addr, _ := testutil.SSH(t)
	m := manager(t)
	r := &eventRecorder{}
	m.SetEventSink(r)
	id := connect(t, m, options(addr, "ssh", "exec"))
	task := run(t, m, "exec", Request{Session: id, Command: "hang", CollectOutput: noCollect()})
	s, _ := m.Get(id)
	s.disconnect("test transport failure")
	found := false
	for _, e := range r.snapshot() {
		if e.Handle == task.Handle && !e.Status.Connected && e.Status.State == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatal("transport failure was not archived as unknown")
	}
}

func TestHistoryOmitsEnvironmentFromExecRejectionMetadata(t *testing.T) {
	r := &eventRecorder{}
	s := &Session{events: r, id: "s1", handle: "task1", failure: "ssh: command bash -c 'export SECRET=environment-secret; command' failed"}
	receipt := Result{Error: s.failure, InputState: "unknown"}
	s.record("receipt", nil, "", "", &receipt)
	b, _ := json.Marshal(r.snapshot())
	if bytes.Contains(b, []byte("environment-secret")) {
		t.Fatal("wire environment leaked into metadata")
	}
	if !strings.Contains(receipt.Error, "environment-secret") {
		t.Fatal("event projection changed original result")
	}
}
