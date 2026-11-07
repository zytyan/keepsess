package core

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type FileRequest struct {
	Session   string `json:"session"`
	Src       string `json:"src"`
	Dst       string `json:"dst"`
	Backend   string `json:"backend"`
	Overwrite bool   `json:"overwrite"`
}

// auxiliary pins the transport against reconnect and idle collection. Cancellation
// closes the shared transport because SSH channel opens/control requests can block.
func (m *Manager) auxiliary(ctx context.Context, id string, writing bool) (*Session, *ssh.Client, func(), error) {
	s, e := m.Get(id)
	if e != nil {
		return nil, nil, nil, e
	}
	if writing && !s.writeMu.TryRLock() {
		return nil, nil, nil, errors.New("busy: configuration changing")
	}
	s.mu.Lock()
	if !s.connected || s.reconnecting || s.protocol != "ssh" || s.sshClient == nil || (writing && s.config.ReadOnly) {
		s.mu.Unlock()
		if writing {
			s.writeMu.RUnlock()
		}
		return nil, nil, nil, errors.New("SSH connection required; reconnect if disconnected; read_only blocks writes/tunnels")
	}
	c := s.sshClient
	s.active++
	s.lastUse = time.Now()
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	return s, c, func() {
		stop()
		s.touch(-1)
		if writing {
			s.writeMu.RUnlock()
		}
	}, nil
}
func shellPath(p string) string {
	if strings.HasPrefix(p, "-") {
		p = "./" + p
	}
	return "'" + strings.ReplaceAll(p, "'", "'\\''") + "'"
}
func validFilePath(p string) bool { return p != "" && !strings.ContainsAny(p, "\x00\r\n") }

func (m *Manager) Transfer(ctx context.Context, put bool, r FileRequest) (map[string]any, error) {
	if !validFilePath(r.Src) || (put && !validFilePath(r.Dst)) || (r.Dst != "" && !validFilePath(r.Dst)) {
		return nil, errors.New("src/dst must be file paths without NUL/newlines")
	}
	if r.Backend == "" {
		r.Backend = "auto"
	}
	if r.Backend != "auto" && r.Backend != "sftp" && r.Backend != "scp" && r.Backend != "exec" {
		return nil, errors.New("backend must be auto/sftp/scp/exec")
	}
	ctx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	s, c, done, e := m.auxiliary(ctx, r.Session, put)
	if e != nil {
		return nil, e
	}
	defer done()
	result := map[string]any{"session": s.id, "src": r.Src, "dst": r.Dst, "complete": false, "bytes_transferred": int64(0)}
	// Only backend availability is probed. Never retry a transfer after it starts.
	backend := r.Backend
	var sf *sftp.Client
	if backend == "auto" || backend == "sftp" {
		sf, e = sftp.NewClient(c)
		if e == nil {
			backend = "sftp"
			defer sf.Close()
		} else if backend == "sftp" {
			return nil, e
		}
	}
	if backend == "auto" {
		probe, err := c.NewSession()
		if err != nil {
			return nil, err
		}
		err = probe.Run("command -v scp >/dev/null 2>&1")
		probe.Close()
		if err == nil && (!put || r.Overwrite) {
			backend = "scp"
		} else {
			backend = "exec"
		}
	}
	e = nil
	result["backend"] = backend
	var local *os.File
	var temp string
	if put {
		local, e = os.Open(r.Src)
		if e == nil {
			var st os.FileInfo
			st, e = local.Stat()
			if e == nil && !st.Mode().IsRegular() {
				e = errors.New("source must be a regular file")
			}
		}
	} else {
		if r.Dst == "" {
			var d string
			d, e = os.MkdirTemp("", "keepsess-download-")
			if e == nil {
				r.Dst = filepath.Join(d, filepath.Base(r.Src))
				result["dst"] = r.Dst
			}
		}
		if e == nil {
			r.Dst, e = filepath.Abs(r.Dst)
			result["dst"] = r.Dst
		}
		if e == nil && !r.Overwrite {
			if _, err := os.Lstat(r.Dst); err == nil {
				e = errors.New("destination exists")
			} else if !os.IsNotExist(err) {
				e = err
			}
		}
		if e == nil {
			local, e = os.CreateTemp(filepath.Dir(r.Dst), ".keepsess-*")
			if e == nil {
				temp = local.Name()
				defer os.Remove(temp)
			}
		}
	}
	if e != nil {
		return nil, e
	}
	defer local.Close()
	var n int64
	finalDst := r.Dst
	remoteTemp := ""
	if put {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		remoteTemp = path.Join(path.Dir(r.Dst), ".keepsess-"+hex.EncodeToString(nonce[:]))
		r.Dst = remoteTemp
		result["temporary_path"] = remoteTemp
	}
	if backend == "sftp" {
		if put {
			flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
			var f *sftp.File
			f, e = sf.OpenFile(r.Dst, flags)
			if e == nil {
				n, e = io.Copy(f, local)
				ce := f.Close()
				if e == nil {
					e = ce
				}
			}
		} else {
			var f *sftp.File
			f, e = sf.Open(r.Src)
			if e == nil {
				var st os.FileInfo
				st, e = f.Stat()
				if e == nil && !st.Mode().IsRegular() {
					e = errors.New("source must be regular")
				}
				if e == nil {
					n, e = io.Copy(local, f)
				}
				f.Close()
			}
		}
	} else {
		n, e = transferCommand(c, backend, put, r, local)
	}
	if e == nil && put {
		if backend == "sftp" {
			if r.Overwrite {
				e = sf.PosixRename(remoteTemp, finalDst)
			} else {
				e = sf.Link(remoteTemp, finalDst)
				if e == nil {
					e = sf.Remove(remoteTemp)
				}
			}
		} else {
			commit, err := c.NewSession()
			if err != nil {
				e = err
			} else {
				cmd := "mv -f " + shellPath(remoteTemp) + " " + shellPath(finalDst)
				if !r.Overwrite {
					cmd = "ln " + shellPath(remoteTemp) + " " + shellPath(finalDst) + " && rm -f " + shellPath(remoteTemp)
				}
				e = commit.Run("test ! -d " + shellPath(finalDst) + " && " + cmd)
				commit.Close()
			}
		}
		if e == nil {
			delete(result, "temporary_path")
		}
	}
	result["bytes_transferred"] = n
	if e == nil && !put {
		e = local.Close()
		if e == nil {
			if r.Overwrite {
				e = os.Rename(temp, r.Dst)
			} else {
				e = os.Link(temp, r.Dst)
			}
		}
	}
	if e != nil {
		result["error"] = e.Error()
		result["warning"] = "Partial transfer or remote outcome may be unknown. Inspect sess_list and explicitly sess_reconnect if disconnected; do not automatically replay."
		return result, nil
	}
	result["complete"] = true
	return result, nil
}

func transferCommand(c *ssh.Client, backend string, put bool, r FileRequest, local *os.File) (int64, error) {
	sh, e := c.NewSession()
	if e != nil {
		return 0, e
	}
	defer sh.Close()
	in, e := sh.StdinPipe()
	if e != nil {
		return 0, e
	}
	out, e := sh.StdoutPipe()
	if e != nil {
		return 0, e
	}
	// Drain diagnostics without retaining unbounded remote output.
	sh.Stderr = io.Discard
	if backend == "exec" {
		cmd := "test -f " + shellPath(r.Src) + " && cat < " + shellPath(r.Src)
		if put {
			cmd = "umask 077; "
			if !r.Overwrite {
				cmd += "set -C; "
			}
			st, err := local.Stat()
			if err != nil {
				return 0, err
			}
			cmd += "cat > " + shellPath(r.Dst) + " && test \"$(wc -c < " + shellPath(r.Dst) + ")\" -eq " + strconv.FormatInt(st.Size(), 10)
		}
		if e = sh.Start(cmd); e != nil {
			return 0, e
		}
		var n int64
		if put {
			n, e = io.Copy(in, local)
			in.Close()
		} else {
			in.Close()
			n, e = io.Copy(local, out)
		}
		wait := sh.Wait()
		if e == nil {
			e = wait
		}
		return n, e
	}
	cmd := "scp -f " + shellPath(r.Src)
	if put {
		cmd = "scp -t " + shellPath(r.Dst)
	}
	if e = sh.Start(cmd); e != nil {
		return 0, e
	}
	br := bufio.NewReaderSize(out, 4096)
	ack := func() error {
		b, e := br.ReadByte()
		if e != nil {
			return e
		}
		if b != 0 {
			return errors.New("scp peer rejected transfer")
		}
		return nil
	}
	var n int64
	if put {
		if e = ack(); e != nil {
			return n, e
		}
		st, e := local.Stat()
		if e != nil {
			return n, e
		}
		fmt.Fprintf(in, "C0600 %d file\n", st.Size())
		if e = ack(); e != nil {
			return n, e
		}
		n, e = io.CopyN(in, local, st.Size())
		if e != nil {
			return n, e
		}
		in.Write([]byte{0})
		e = ack()
	} else {
		in.Write([]byte{0})
		line, err := br.ReadSlice('\n')
		if err != nil {
			return 0, err
		}
		fields := strings.SplitN(strings.TrimSuffix(string(line), "\n"), " ", 3)
		if len(fields) != 3 || len(fields[0]) != 5 || fields[0][0] != 'C' {
			return 0, errors.New("invalid SCP file header")
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || size < 0 {
			return 0, errors.New("invalid SCP size")
		}
		in.Write([]byte{0})
		n, e = io.CopyN(local, br, size)
		if e == nil {
			e = ack()
		}
		if e == nil {
			_, e = in.Write([]byte{0})
		}
	}
	in.Close()
	wait := sh.Wait()
	if e == nil {
		e = wait
	}
	return n, e
}
