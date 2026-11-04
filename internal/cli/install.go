package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const installConfigLimit = 4 << 20

type installOptions struct {
	client, file, dir, skillDir string
	dryRun                      bool
}
type installPaths struct{ client, config, binary, skill string }
type installFile struct {
	path             string
	data             []byte
	mode             os.FileMode
	oldMode          os.FileMode
	old              []byte
	existed, changed bool
	backup           string
}

func installCommand(args []string, out, errout io.Writer) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(errout)
	var opts installOptions
	flags.StringVar(&opts.client, "client", "auto", "auto, claude, cursor, vscode, codex or json")
	flags.StringVar(&opts.dir, "dir", "", "Managed binary directory (default project/.sessmcp)")
	flags.StringVar(&opts.skillDir, "skill-dir", "", "Skill destination directory; defaults to client skill location")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "Print destinations without writing any files")
	// Permit the positional file before or after flags, matching install [file].
	var normalized, files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			normalized = append(normalized, a)
			if a == "--client" || a == "-client" || a == "--dir" || a == "-dir" || a == "--skill-dir" || a == "-skill-dir" {
				if i+1 == len(args) {
					return fmt.Errorf("missing value for %s", a)
				}
				i++
				normalized = append(normalized, args[i])
			}
		} else {
			files = append(files, a)
		}
	}
	if err := flags.Parse(normalized); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected install arguments")
	}
	if len(files) > 1 {
		return errors.New("install accepts at most one target configuration file")
	}
	if len(files) == 1 {
		opts.file = files[0]
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	return installAt(opts, source, out)
}
func resolveInstall(opts installOptions, sourceHash string) (installPaths, error) {
	switch opts.client {
	case "auto", "claude", "cursor", "vscode", "codex", "json":
	default:
		return installPaths{}, errors.New("unknown client; use auto, claude, cursor, vscode, codex or json")
	}
	file := opts.file
	if file == "" {
		switch opts.client {
		case "cursor":
			file = filepath.Join(".cursor", "mcp.json")
		case "vscode":
			file = filepath.Join(".vscode", "mcp.json")
		case "codex":
			file = filepath.Join(".codex", "config.toml")
		default:
			file = ".mcp.json"
		}
	}
	file, err := installAbs(file)
	if err != nil {
		return installPaths{}, err
	}
	client := opts.client
	parent := filepath.Dir(file)
	if client == "auto" {
		if strings.EqualFold(filepath.Ext(file), ".toml") {
			client = "codex"
		} else {
			data, _, err := readInstallFile(file, installConfigLimit)
			if err != nil {
				return installPaths{}, err
			}
			v, err := parseInstallJSON(data)
			if err != nil {
				return installPaths{}, err
			}
			servers, mcp := v.Find("/servers") != nil, v.Find("/mcpServers") != nil
			if servers && mcp {
				return installPaths{}, errors.New("both servers and mcpServers exist; specify --client")
			}
			switch {
			case servers:
				client = "vscode"
			case filepath.Base(parent) == ".cursor":
				client = "cursor"
			case filepath.Base(parent) == ".vscode":
				client = "vscode"
			case filepath.Base(file) == ".mcp.json" || filepath.Base(file) == ".claude.json":
				client = "claude"
			default:
				client = "json"
			}
		}
	}
	if client != "codex" && strings.EqualFold(filepath.Ext(file), ".toml") {
		return installPaths{}, errors.New("TOML target requires --client codex")
	}
	root := parent
	switch filepath.Base(parent) {
	case ".cursor", ".vscode", ".codex":
		root = filepath.Dir(parent)
	}
	dir := opts.dir
	if dir == "" {
		dir = filepath.Join(root, ".sessmcp")
	}
	dir, err = installAbs(dir)
	if err != nil {
		return installPaths{}, err
	}
	skillDir := opts.skillDir
	if skillDir == "" {
		switch client {
		case "claude":
			skillDir = filepath.Join(root, ".claude", "skills", "sessmcp")
		case "cursor":
			skillDir = filepath.Join(root, ".cursor", "skills", "sessmcp")
		case "vscode":
			skillDir = filepath.Join(root, ".github", "skills", "sessmcp")
		default:
			skillDir = filepath.Join(root, ".agents", "skills", "sessmcp")
		}
	}
	skillDir, err = installAbs(skillDir)
	if err != nil {
		return installPaths{}, err
	}
	executable := "sessmcp"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	return installPaths{client: client, config: file, binary: filepath.Join(dir, "bin", sourceHash, executable), skill: filepath.Join(skillDir, "SKILL.md")}, nil
}
func readInstallFile(path string, limit int64) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("installation target must be a regular file, not a directory or symlink: %s", path)
	}
	if info.Size() > limit {
		return nil, false, fmt.Errorf("installation file exceeds size limit: %s", path)
	}
	data, err := os.ReadFile(path)
	return data, true, err
}
func installAt(opts installOptions, source string, out io.Writer) error {
	// Content-addressed executable paths avoid replacing a running Windows EXE
	// and keep an old configuration functional until the new config is committed.
	binary, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(binary)
	paths, err := resolveInstall(opts, hex.EncodeToString(sum[:16]))
	if err != nil {
		return err
	}
	config, _, err := readInstallFile(paths.config, installConfigLimit)
	if err != nil {
		return err
	}
	var merged []byte
	if paths.client == "codex" {
		merged, err = installTOML(config, paths.binary)
	} else {
		merged, err = installJSON(config, paths.client, paths.binary)
	}
	if err != nil {
		return err
	}
	files := []installFile{{path: paths.binary, data: binary, mode: 0755}, {path: paths.skill, data: []byte(Skill), mode: 0644}, {path: paths.config, data: merged, mode: 0600}}
	seen := map[string]bool{}
	for i := range files {
		f := &files[i]
		if seen[f.path] {
			return errors.New("installation paths collide")
		}
		seen[f.path] = true
		f.old, f.existed, err = readInstallFile(f.path, 128<<20)
		if err != nil {
			return err
		}
		f.changed = !f.existed || !bytes.Equal(f.old, f.data)
		if f.existed {
			info, e := os.Stat(f.path)
			if e != nil {
				return e
			}
			f.oldMode = info.Mode().Perm()
			if i == 0 && runtime.GOOS != "windows" && f.oldMode&0111 == 0 {
				f.changed = true
			}
		}
	}
	if opts.dryRun {
		_, err = fmt.Fprintf(out, "Client: %s\nConfig: %s\nExecutable: %s\nSkill: %s\nDry run: no files written.\n", paths.client, paths.config, paths.binary, paths.skill)
		return err
	}
	// Lock the shared config, not just one managed binary directory.
	if err = os.MkdirAll(filepath.Dir(paths.config), 0755); err != nil {
		return err
	}
	lockPath := paths.config + ".sessmcp-install.lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("cannot acquire installation lock %s: %w", lockPath, err)
	}
	_ = lock.Close()
	defer os.Remove(lockPath)
	// Detect a concurrent edit after planning. Do not overwrite another editor.
	for i := range files {
		f := &files[i]
		now, exists, e := readInstallFile(f.path, 128<<20)
		if e != nil {
			return e
		}
		if exists != f.existed || !bytes.Equal(now, f.old) {
			return errors.New("installation files changed while planning; rerun install")
		}
	}
	if err = commitInstall(files, atomicInstallWrite); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Installed sessmcp for %s.\nConfig: %s\nExecutable: %s\nSkill: %s\n", paths.client, paths.config, paths.binary, paths.skill)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.backup != "" {
			if _, err = fmt.Fprintf(out, "Backup: %s\n", f.backup); err != nil {
				return err
			}
		}
	}
	_, err = fmt.Fprintln(out, "Reload the client's MCP configuration; approve/trust it in the client if requested. No service or permission rules were started/changed.")
	return err
}
func commitInstall(files []installFile, write func(string, []byte, os.FileMode) error) error {
	for i := range files {
		f := &files[i]
		if !f.changed {
			continue
		}
		if f.existed {
			if err := os.MkdirAll(filepath.Dir(f.path), 0755); err != nil {
				return err
			}
			backup, err := os.CreateTemp(filepath.Dir(f.path), filepath.Base(f.path)+".sessmcp-backup-*")
			if err != nil {
				return err
			}
			f.backup = backup.Name()
			_, err = backup.Write(f.old)
			if err == nil {
				err = backup.Sync()
			}
			closeErr := backup.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
		}
	}
	written := []int{}
	for i := range files {
		f := &files[i]
		if !f.changed {
			continue
		}
		if err := write(f.path, f.data, f.mode); err != nil {
			failures := []error{fmt.Errorf("install %s: %w", f.path, err)}
			for j := len(written) - 1; j >= 0; j-- {
				old := &files[written[j]]
				var restore error
				if old.existed {
					mode := old.oldMode
					if mode == 0 {
						mode = old.mode
					}
					restore = atomicInstallWrite(old.path, old.old, mode)
				} else {
					restore = os.Remove(old.path)
				}
				if restore != nil {
					failures = append(failures, fmt.Errorf("rollback %s: %w", old.path, restore))
				}
			}
			return errors.Join(failures...)
		}
		written = append(written, i)
	}
	return nil
}
func atomicInstallWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".sessmcp-install-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	return nil
}

func installAbs(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	return filepath.Abs(path)
}
