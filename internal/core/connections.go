package core

import "time"

// ConnectionEndpoint is private connection metadata. Never add it to Event or Result.
// Deliberately excludes private key material, key passphrases and application input.
type ConnectionEndpoint struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	HasPassword bool   `json:"has_password"`
	Keyfile     string `json:"keyfile,omitempty"`
	KnownHosts  string `json:"known_hosts,omitempty"`
	HostKey     string `json:"host_key,omitempty"`
	Insecure    bool   `json:"insecure"`
}

// ConnectionRecord travels only to the private local persistence sink.
type ConnectionRecord struct {
	Version        int                  `json:"version"`
	Instance       string               `json:"instance"`
	Session        string               `json:"session"`
	Name           string               `json:"name,omitempty"`
	Protocol       string               `json:"protocol"`
	Mode           string               `json:"mode"`
	FirstConnected time.Time            `json:"first_connected"`
	LastConnected  time.Time            `json:"last_connected"`
	Target         ConnectionEndpoint   `json:"target"`
	SSHJumps       []ConnectionEndpoint `json:"ssh_jumps,omitempty"`
	Manual         bool                 `json:"manual"`
}

// ConnectionSink must copy/own its input and return promptly without disk I/O.
// Install before serving, alongside the event sink.
type ConnectionSink interface{ RecordConnection(ConnectionRecord) error }

// SetConnectionSink installs private persistence without enabling terminal history.
// Call before serving. A regular history Store is also detected by SetEventSink.
func (m *Manager) SetConnectionSink(sink ConnectionSink) { m.connections = sink }

func connectionEndpoint(o Options) ConnectionEndpoint {
	return ConnectionEndpoint{Host: o.Host, Port: o.Port, Username: o.Username, Password: o.Password,
		HasPassword: o.Password != "", Keyfile: o.Keyfile, KnownHosts: o.KnownHosts, HostKey: o.HostKey, Insecure: o.Insecure}
}

// Called under m.mu after publishing a successful connection, before close can invalidate it.
func (m *Manager) recordConnectionLocked(s *Session, o Options) string {
	if m.connections == nil {
		return ""
	}
	s.mu.Lock()
	name := s.name
	s.mu.Unlock()
	now := time.Now().UTC()
	r := ConnectionRecord{Version: 1, Instance: m.instanceID, Session: s.id, Name: name, Protocol: o.Protocol,
		Mode: o.Mode, FirstConnected: now, LastConnected: now, Target: connectionEndpoint(o)}
	for _, jump := range o.SSHJumps {
		r.SSHJumps = append(r.SSHJumps, connectionEndpoint(jump.options()))
	}
	if m.connections.RecordConnection(r) != nil {
		return "Connection succeeded, but private connection information was not queued for saving; check connection management storage status."
	}
	return ""
}
