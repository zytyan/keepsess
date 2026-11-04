// Package buildinfo exposes the same build identity to CLI and MCP clients.
package buildinfo

import "runtime/debug"

const Version = "0.2.0"

// Revision is injected by scripts/build.sh; go build may supply VCS metadata.
var Revision = "unknown"

func String() string {
	revision := Revision
	if revision == "unknown" {
		if info, ok := debug.ReadBuildInfo(); ok {
			dirty := false
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					revision = setting.Value
				}
				if setting.Key == "vcs.modified" {
					dirty = setting.Value == "true"
				}
			}
			if dirty {
				revision += "-dirty"
			}
		}
	}
	return Version + "+" + revision
}
