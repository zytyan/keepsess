package settings

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/zytyan/keepsess/internal/core"
)

// Profiles are explicit connection recipes, separate from observed connection
// history. Key material/passphrases, live handles and terminal input are absent.
type ProfileEndpoint struct {
	Host       string `toml:"host"`
	Port       int    `toml:"port"`
	Username   string `toml:"username"`
	Password   string `toml:"password"`
	Keyfile    string `toml:"keyfile"`
	KnownHosts string `toml:"known_hosts"`
	HostKey    string `toml:"host_key"`
	Insecure   bool   `toml:"insecure"`
}
type ConnectionProfile struct {
	Protocol   string            `toml:"protocol"`
	Mode       string            `toml:"mode"`
	Host       string            `toml:"host"`
	Port       int               `toml:"port"`
	Username   string            `toml:"username"`
	Password   string            `toml:"password"`
	Keyfile    string            `toml:"keyfile"`
	KnownHosts string            `toml:"known_hosts"`
	HostKey    string            `toml:"host_key"`
	Insecure   bool              `toml:"insecure"`
	SSHJumps   []ProfileEndpoint `toml:"ssh_jumps"`
}

var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func (c Config) ConnectionProfiles() (map[string]core.Options, error) {
	if len(c.Profiles) > 64 {
		return nil, fmt.Errorf("at most 64 connection profiles are supported")
	}
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make(map[string]core.Options, len(names))
	for _, name := range names {
		p := c.Profiles[name]
		if !profileName.MatchString(name) || name == "last" {
			return nil, fmt.Errorf("invalid profile name %q: use 1..64 ASCII alphanumeric/._-, start alphanumeric; last is reserved", name)
		}
		if (p.Protocol != "ssh" && p.Protocol != "telnet") || p.Host == "" || p.Port < 1 || p.Port > 65535 {
			return nil, fmt.Errorf("profile %q requires protocol=ssh/telnet, host and port=1..65535", name)
		}
		// Explicit mode avoids cold-start target guessing, especially on devices.
		if p.Mode != "exec" && p.Mode != "stream" || p.Protocol == "telnet" && p.Mode != "stream" {
			return nil, fmt.Errorf("profile %q requires explicit mode=exec/stream; Telnet requires stream", name)
		}
		if len(p.SSHJumps) > 8 {
			return nil, fmt.Errorf("profile %q exceeds 8 SSH jumps", name)
		}
		if p.Protocol == "ssh" && p.Username == "" {
			return nil, fmt.Errorf("SSH profile %q requires username", name)
		}
		if p.Protocol == "telnet" && (p.Username != "" || p.Password != "" || p.Keyfile != "") {
			return nil, fmt.Errorf("Telnet profile %q cannot contain target SSH credentials; send login input explicitly", name)
		}
		o := core.Options{Protocol: p.Protocol, Mode: p.Mode, Host: p.Host, Port: p.Port, Username: p.Username, Password: p.Password, Keyfile: expand(c.Home, p.Keyfile), KnownHosts: expand(c.Home, p.KnownHosts), HostKey: p.HostKey, Insecure: p.Insecure}
		for i, j := range p.SSHJumps {
			if j.Host == "" || j.Username == "" || j.Port < 0 || j.Port > 65535 || j.Password == "" && j.Keyfile == "" {
				return nil, fmt.Errorf("profile %q jump %d requires host, username, valid port and password/keyfile", name, i+1)
			}
			o.SSHJumps = append(o.SSHJumps, core.SSHJump{Host: j.Host, Port: j.Port, Username: j.Username, Password: j.Password, Keyfile: expand(c.Home, j.Keyfile), KnownHosts: expand(c.Home, j.KnownHosts), HostKey: j.HostKey, Insecure: j.Insecure})
		}
		out[name] = o
	}
	return out, nil
}
