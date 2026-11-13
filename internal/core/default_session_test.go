package core

import (
	"context"
	"strings"
	"testing"

	"github.com/zytyan/keepsess/internal/testutil"
)

func TestExecUniqueSession(t *testing.T) {
	m := manager(t)
	ctx := context.Background()
	req := Request{Command: "both"}
	checkError := func(r Request, want string) {
		t.Helper()
		if _, err := m.Operate(ctx, "exec", r); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
	checkError(req, "connect")
	addr, _ := testutil.SSH(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	v := run(t, m, "exec", req)
	if v.Session != id || v.ExitCode == nil || *v.ExitCode != 0 || !strings.Contains(v.Output, "stdout") || !strings.Contains(v.Output, "stderr") {
		t.Fatal(v)
	}
	// An explicit invalid target must never fall back to the sole session.
	checkError(Request{Session: "missing", Command: "both"}, "unavailable")
	stream := connect(t, m, options(addr, "ssh", "stream"))
	checkError(req, "multiple retained sessions")
	s, err := m.Get(stream)
	if err != nil {
		t.Fatal(err)
	}
	s.disconnect("test disconnect")
	// Disconnected and stream sessions still count toward ambiguity.
	checkError(req, "multiple retained sessions")
	v = run(t, m, "exec", Request{Session: id, Command: "both"})
	if v.Session != id || v.ExitCode == nil {
		t.Fatal(v)
	}
	tasks, err := m.TaskList("")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("rejected calls started tasks: %v, %v", tasks, err)
	}
	if _, err := m.Close(stream); err != nil {
		t.Fatal(err)
	}
	s, err = m.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	s.disconnect("test disconnect")
	v = run(t, m, "exec", req)
	if v.Session != id || v.Connected || v.InputState != "not_sent" || v.NextAction != "reconnect" {
		t.Fatal(v)
	}
	m.CloseAll()
	checkError(req, "connect")
}

func TestExecUniqueStreamSessionRejected(t *testing.T) {
	addr, _ := testutil.SSH(t)
	m := manager(t)
	connect(t, m, options(addr, "ssh", "stream"))
	if _, err := m.Operate(context.Background(), "exec", Request{Command: "both"}); err == nil || !strings.Contains(err.Error(), "requires SSH mode=exec") {
		t.Fatal(err)
	}
}
