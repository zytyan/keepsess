package core

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func TestFileExecAndSCPBinary(t *testing.T) {
	addr, _ := testutil.SSHWithExec(t, func(cmd string, ch ssh.Channel, _ <-chan *ssh.Request) {
		c := exec.Command("sh", "-c", cmd)
		c.Stdin = ch
		c.Stdout = ch
		c.Stderr = ch.Stderr()
		err := c.Run()
		code := uint32(0)
		if err != nil {
			code = 1
		}
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{code}))
	})
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	dir := t.TempDir()
	content := bytes.Repeat([]byte{0x3f, 0, 0, 0, 255, '\r', '\n'}, 30000)
	src := filepath.Join(dir, "local")
	os.WriteFile(src, content, 0600)
	for _, backend := range []string{"exec", "scp", "auto"} {
		if backend == "scp" {
			if _, e := exec.LookPath("scp"); e != nil {
				continue
			}
		}
		remote := filepath.Join(dir, "remote ' "+backend)
		r, e := m.Transfer(context.Background(), true, FileRequest{Session: id, Src: src, Dst: remote, Backend: backend, Overwrite: false})
		if e != nil || r["complete"] != true {
			t.Fatalf("%s: %v %v", backend, r, e)
		}
		dst := filepath.Join(dir, "download-"+backend)
		r, e = m.Transfer(context.Background(), false, FileRequest{Session: id, Src: remote, Dst: dst, Backend: backend})
		if e != nil || r["complete"] != true {
			t.Fatalf("%s: %v %v", backend, r, e)
		}
		again, err := m.Transfer(context.Background(), true, FileRequest{Session: id, Src: src, Dst: remote, Backend: backend})
		if err == nil && again["complete"] == true {
			t.Fatal("upload overwrote existing destination")
		}
		if again != nil {
			if temp, ok := again["temporary_path"].(string); ok {
				os.Remove(temp)
			}
		}
		got, _ := os.ReadFile(dst)
		if !bytes.Equal(got, content) {
			t.Fatal("binary changed")
		}
		if _, e = m.Transfer(context.Background(), false, FileRequest{Session: id, Src: remote, Dst: dst, Backend: "exec"}); e == nil {
			t.Fatal("clobbered destination")
		}
	}
	m.Configure(id, cfg(map[string]any{"read_only": true}))
	if _, e := m.Transfer(context.Background(), true, FileRequest{Session: id, Src: src, Dst: src, Backend: "exec"}); e == nil {
		t.Fatal("read_only upload allowed")
	}
}

func TestSOCKSHandshake(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		defer b.Close()
		got, e := socksTarget(b)
		if e != nil || got != "localhost:5556" {
			t.Errorf("%s %v", got, e)
		}
	}()
	a.Write([]byte{5, 1, 0})
	var reply [2]byte
	io.ReadFull(a, reply[:])
	if reply != [2]byte{5, 0} {
		t.Fatal(reply)
	}
	a.Write(append([]byte{5, 1, 0, 3, 9}, append([]byte("localhost"), 0x15, 0xb4)...))
	a.Close()
}

func TestFileCancellationClosesTransport(t *testing.T) {
	addr, _ := testutil.SSHWithExec(t, func(_ string, ch ssh.Channel, reqs <-chan *ssh.Request) {
		for range reqs {
		}
	})
	m := manager(t)
	id := connect(t, m, options(addr, "ssh", "exec"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r, e := m.Transfer(ctx, false, FileRequest{Session: id, Src: "/stalled", Dst: filepath.Join(t.TempDir(), "out"), Backend: "exec"})
	if e == nil && r["complete"] == true {
		t.Fatal("cancelled transfer completed")
	}
	if ctx.Err() == nil {
		t.Fatal("expected deadline")
	}
}
