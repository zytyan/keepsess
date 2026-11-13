package mcp

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/zytyan/keepsess/internal/core"
)

func (s *Server) profileNames() []string {
	names := make([]string, 0, len(s.Profiles))
	for name := range s.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *Server) profileOptions(o core.Options, keys map[string]json.RawMessage) (core.Options, error) {
	if keys["profile"] == nil {
		for _, key := range []string{"protocol", "host", "port"} {
			if keys[key] == nil {
				return o, argumentError("connect", key, true)
			}
		}
		return o, nil
	}
	if o.Profile == "" {
		return o, fmt.Errorf("profile must be a nonempty explicit name; no connection attempted")
	}
	p, ok := s.Profiles[o.Profile]
	if !ok {
		return o, fmt.Errorf("unknown connection profile %q; available_profiles=%v; define [profiles.NAME] in ~/.keepsess.toml and restart this Agent to load it; no connection attempted", o.Profile, s.profileNames())
	}
	// Never silently change a recipe's destination, route, protocol or host trust.
	for _, key := range []string{"protocol", "host", "port", "mode", "username", "ssh_jumps", "known_hosts", "host_key", "insecure"} {
		if keys[key] != nil {
			return o, fmt.Errorf("profile cannot be combined with %s; use the named recipe or provide a complete direct connection; nothing sent", key)
		}
	}
	p.Profile = ""
	p.Name, p.Config = o.Name, o.Config
	p.SSHJumps = append([]core.SSHJump(nil), p.SSHJumps...)
	// Explicit per-call authentication replaces only supplied values, not routing.
	if keys["password"] != nil {
		p.Password = o.Password
	}
	if keys["keyfile"] != nil {
		p.Keyfile = o.Keyfile
	}
	if keys["key_passphrase"] != nil {
		p.KeyPassphrase = o.KeyPassphrase
	}
	return p, nil
}
