package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func directCall(t *testing.T, s *Server, tool string, args map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"name": tool, "arguments": args})
	v := s.call(context.Background(), b).(map[string]any)
	// Exercise the actual wire representation, including typed Result values.
	b, _ = json.Marshal(v["structuredContent"])
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestActionableArgumentErrorsDoNotStartTasks(t *testing.T) {
	s := &Server{Manager: core.NewManager()}
	defer s.Manager.CloseAll()
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"exec", map[string]any{"command": "secret command", "exec_timeout_ms": 12}, "exec_total_timeout_ms"},
		{"exec", map[string]any{"command": "secret command", "max_output_tokens": 12}, "Bytes are not tokens"},
		{"task_signal", map[string]any{"task": "task1", "signal": "TERM"}, "handle"},
		{"task_signal", map[string]any{"handle": "task1"}, "signal"},
		{"connect", map[string]any{}, "protocol"},
		{"sendline_text", map[string]any{"session": "s1", "text": "secret password"}, "data"},
	} {
		v := directCall(t, s, tc.tool, tc.args)
		message, _ := v["error"].(string)
		if !strings.Contains(message, tc.want) || !strings.Contains(message, "example:") || !strings.Contains(message, "No application input") || strings.Contains(message, "secret") {
			t.Fatal(tc.tool, v)
		}
	}
	if len(s.Manager.List()) != 0 {
		t.Fatal("invalid arguments created sessions")
	}
	for _, name := range []string{"sess_exec", "keepsess_sess_exec", "keepsess_exec"} {
		v := directCall(t, s, name, map[string]any{"command": "secret"})
		if !strings.Contains(v["error"].(string), `server tool name is "exec"`) {
			t.Fatal(v)
		}
	}
	for _, raw := range Tools() {
		if strings.HasPrefix(raw.(map[string]any)["name"].(string), "sess_") {
			t.Fatal("old prefix discovered")
		}
	}
}

func TestOutputGuidancePreservesReadRangeAndDoesNotReplay(t *testing.T) {
	for _, tool := range []string{"exec", "read_until", "read_history", "read_tail"} {
		v := explainResult(core.Result{Session: "s1", Handle: "task1", Reason: "output_limit", More: true, End: 84, MinimumOutputBytes: 8}, tool, map[string]json.RawMessage{"strip_ansi": json.RawMessage("true")})
		if v.NextArguments["handle"] != "task1" || v.NextArguments["session"] != nil || v.NextArguments["max_output_bytes"] != 8 || v.NextArguments["strip_ansi"] != true {
			t.Fatal(v)
		}
		if tool == "read_history" || tool == "read_tail" {
			if v.NextAction != "read_history" || v.NextArguments["offset"] != int64(84) {
				t.Fatal(v)
			}
		} else if v.NextAction != "read_available" {
			t.Fatal(v)
		}
	}
	v := explainResult(core.Result{Error: "busy: synchronous exec call in progress; nothing sent", InputState: "not_sent", Session: "s1"}, "exec", nil)
	if v.NextAction != "task_list" || !strings.Contains(v.Guidance, "Wait") {
		t.Fatal(v)
	}
	v = explainResult(core.Result{Error: "resource_limit: running task limit reached; nothing sent"}, "exec", nil)
	if v.NextAction != "task_list" {
		t.Fatal(v)
	}
	v = explainResult(core.Result{Reason: "output_limit", NextAction: "reconnect", DroppedBytes: 3}, "exec", nil)
	if v.NextAction != "reconnect" {
		t.Fatal("lost transport guidance", v)
	}
}

func TestNamedProfileCreatesNewSessionsWithoutCredentialDisclosure(t *testing.T) {
	addr, _ := testutil.SSH(t)
	args := connectArgs(addr)
	p := core.Options{Protocol: "ssh", Host: args["host"].(string), Port: args["port"].(int), Username: "test", Password: "private-profile-password", Mode: "exec"}
	s := &Server{Manager: core.NewManager(), Profiles: map[string]core.Options{"server": p}}
	defer s.Manager.CloseAll()
	first := directCall(t, s, "connect", map[string]any{"profile": "server", "name": "prod"})
	second := directCall(t, s, "connect", map[string]any{"profile": "server"})
	if first["error"] != nil || first["session"] == second["session"] || second["error"] != nil {
		t.Fatal(first, second)
	}
	for _, args := range []map[string]any{{"profile": "server", "host": "other"}, {"profile": "server", "insecure": false}, {"profile": "last"}, {"profile": nil}} {
		if v := directCall(t, s, "connect", args); v["error"] == nil {
			t.Fatal(v)
		}
	}
	if len(s.Manager.List()) != 2 {
		t.Fatal("rejected profile opened session")
	}
	unknown := directCall(t, s, "get_config", map[string]any{"session": "missing"})
	if len(unknown["available_profiles"].([]any)) != 1 || unknown["instance_id"] == nil || strings.Contains(unknown["error"].(string), "process restarted") {
		t.Fatal(unknown)
	}
	listed := directCall(t, s, "list", map[string]any{})
	b, _ := json.Marshal([]any{first, second, unknown, listed})
	if strings.Contains(string(b), p.Password) || listed["api_contract"] != APIContract {
		t.Fatal("profile credential leak or missing API identity")
	}
}

func TestContinuationArgumentsReadFilteredOutputWithoutExecutingAgain(t *testing.T) {
	var executions atomic.Int32
	payload := strings.Repeat("drop this line\nkeep this line\n", 40)
	addr, _ := testutil.SSHWithExec(t, func(_ string, ch ssh.Channel, _ <-chan *ssh.Request) {
		executions.Add(1)
		_, _ = io.WriteString(ch, payload)
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
	})
	s := &Server{Manager: core.NewManager()}
	defer s.Manager.CloseAll()
	args := connectArgs(addr)
	args["protocol"] = "ssh"
	args["username"] = "user"
	args["password"] = "test"
	conn := directCall(t, s, "connect", args)
	page := directCall(t, s, "exec", map[string]any{"session": conn["session"], "command": "payload", "include_pattern": "^keep", "max_output_bytes": 32})
	output, _ := page["output"].(string)
	for i := 0; i < 100 && page["next_action"] != nil; i++ {
		action := page["next_action"].(string)
		if action != "read_available" {
			t.Fatal(page)
		}
		next := page["next_arguments"].(map[string]any)
		if next["include_pattern"] != "^keep" || next["handle"] == nil {
			t.Fatal("projection/target lost", page)
		}
		page = directCall(t, s, action, next)
		part, _ := page["output"].(string)
		output += part
	}
	if output != strings.Repeat("keep this line\n", 40) || executions.Load() != 1 {
		t.Fatal("continuation lost output or replayed input", len(output), executions.Load(), page)
	}
}
