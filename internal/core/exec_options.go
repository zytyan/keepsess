package core

import (
	"errors"
	"regexp"
	"sort"
	"strings"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

// Explicit overrides run after login initialization. The original command is
// interpreted once by Bash; the quoting layer protects cwd and environment data.
func execCommand(r Request) (string, error) {
	if strings.ContainsRune(r.Command, 0) || strings.ContainsRune(r.Cwd, 0) {
		return "", errors.New("command/cwd cannot contain NUL; nothing sent")
	}
	if len(r.Env) > 128 {
		return "", errors.New("env exceeds 128 entries; nothing sent")
	}
	keys := make([]string, 0, len(r.Env))
	for key, value := range r.Env {
		if !envName.MatchString(key) || strings.ContainsRune(value, 0) {
			return "", errors.New("invalid env name or NUL value; nothing sent")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	login := r.Login == nil || *r.Login
	if !login && len(keys) == 0 && r.Cwd == "" {
		return r.Command, nil
	}
	var payload strings.Builder
	for _, key := range keys {
		payload.WriteString("export " + key + "=" + shellQuote(r.Env[key]) + " || exit $?\n")
	}
	if r.Cwd != "" {
		payload.WriteString("cd -- " + shellQuote(r.Cwd) + " || exit $?\n")
	}
	payload.WriteString(r.Command)
	prefix := "bash -c "
	if login {
		prefix = "bash --login -c "
	}
	return prefix + shellQuote(payload.String()), nil
}
