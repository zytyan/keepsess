package mcp

import "errors"

var standardTools = []string{
	"sess_help", "sess_connect", "sess_reconnect", "sess_configure", "sess_list",
	"sess_exec", "sess_task_list", "sess_task_wait", "sess_task_signal", "sess_task_close_stdin",
	"sess_send_text", "sess_send_escaped", "sess_sendline_text", "sess_read_available", "sess_read_until",
	"sess_get_file", "sess_put_file", "sess_close",
}

func ValidToolProfile(profile string) bool { return profile == "standard" || profile == "full" }
func (s *Server) toolProfile() string {
	s.profileMu.Lock()
	defer s.profileMu.Unlock()
	if s.ToolProfile == "full" {
		return "full"
	}
	return "standard"
}
func (s *Server) tools() []any {
	if s.toolProfile() == "full" {
		return Tools()
	}
	out := make([]any, 0, len(standardTools))
	for _, name := range standardTools {
		out = append(out, publicDefinitions[name])
	}
	return out
}
func (s *Server) setToolProfile(profile string) error {
	if !ValidToolProfile(profile) {
		return errors.New("tool_profile must be standard or full")
	}
	s.profileMu.Lock()
	defer s.profileMu.Unlock()
	old := s.ToolProfile
	if old == "" {
		old = "standard"
	}
	s.ToolProfile = profile
	if old != profile && s.toolChanges != nil {
		select {
		case s.toolChanges <- struct{}{}:
		default:
		}
	}
	return nil
}
