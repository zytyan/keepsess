package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/zytyan/keepsess/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func auxiliaryServer(t *testing.T) string {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(key)
	cfg := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	cfg.AddHostKey(signer)
	return testutil.Listen(t, func(raw net.Conn) {
		c, chs, reqs, e := ssh.NewServerConn(raw, cfg)
		if e != nil {
			return
		}
		defer c.Close()
		var mu sync.Mutex
		listeners := map[string]net.Listener{}
		defer func() {
			mu.Lock()
			defer mu.Unlock()
			for _, l := range listeners {
				l.Close()
			}
		}()
		go func() {
			for r := range reqs {
				var a struct {
					Host string
					Port uint32
				}
				ssh.Unmarshal(r.Payload, &a)
				switch r.Type {
				case "tcpip-forward":
					l, e := net.Listen("tcp", net.JoinHostPort(a.Host, fmt.Sprint(a.Port)))
					if e != nil {
						r.Reply(false, nil)
						continue
					}
					port := uint32(l.Addr().(*net.TCPAddr).Port)
					mu.Lock()
					listeners[fmt.Sprint(port)] = l
					mu.Unlock()
					r.Reply(true, ssh.Marshal(struct{ Port uint32 }{port}))
					go func() {
						for {
							v, e := l.Accept()
							if e != nil {
								return
							}
							go func() {
								defer v.Close()
								ch, rs, e := c.OpenChannel("forwarded-tcpip", ssh.Marshal(struct {
									Host       string
									Port       uint32
									Origin     string
									OriginPort uint32
								}{a.Host, port, "127.0.0.1", 1234}))
								if e != nil {
									return
								}
								defer ch.Close()
								go ssh.DiscardRequests(rs)
								bridgeTest(ch, v)
							}()
						}
					}()
				case "cancel-tcpip-forward":
					mu.Lock()
					if l := listeners[fmt.Sprint(a.Port)]; l != nil {
						l.Close()
						delete(listeners, fmt.Sprint(a.Port))
					}
					mu.Unlock()
					r.Reply(true, nil)
				default:
					r.Reply(false, nil)
				}
			}
		}()
		for nc := range chs {
			switch nc.ChannelType() {
			case "direct-tcpip":
				var a struct {
					Host       string
					Port       uint32
					Origin     string
					OriginPort uint32
				}
				ssh.Unmarshal(nc.ExtraData(), &a)
				v, e := net.Dial("tcp", net.JoinHostPort(a.Host, fmt.Sprint(a.Port)))
				if e != nil {
					nc.Reject(ssh.ConnectionFailed, e.Error())
					continue
				}
				ch, rs, e := nc.Accept()
				if e != nil {
					v.Close()
					continue
				}
				go ssh.DiscardRequests(rs)
				go func() { defer v.Close(); defer ch.Close(); bridgeTest(ch, v) }()
			case "session":
				ch, rs, e := nc.Accept()
				if e != nil {
					continue
				}
				go func() {
					defer ch.Close()
					for r := range rs {
						if r.Type == "subsystem" {
							r.Reply(true, nil)
							server, e := sftp.NewServer(ch)
							if e == nil {
								server.Serve()
								server.Close()
							}
							return
						}
						r.Reply(false, nil)
					}
				}()
			default:
				nc.Reject(ssh.UnknownChannelType, "unsupported")
			}
		}
	})
}
func bridgeTest(ch ssh.Channel, c net.Conn) {
	done := make(chan struct{})
	go func() {
		io.Copy(c, ch)
		if w, ok := c.(interface{ CloseWrite() error }); ok {
			w.CloseWrite()
		}
		close(done)
	}()
	io.Copy(ch, c)
	ch.CloseWrite()
	c.Close()
	<-done
}

func TestAllTunnelModesAndSFTP(t *testing.T) {
	echo := testutil.Listen(t, func(c net.Conn) { io.Copy(c, c) })
	host, portText, _ := net.SplitHostPort(echo)
	port, _ := strconv.Atoi(portText)
	m := manager(t)
	id := connect(t, m, options(auxiliaryServer(t), "ssh", "exec"))
	for _, kind := range []string{"local", "remote", "dynamic", "remote_dynamic"} {
		v, e := m.Tunnel(context.Background(), "open", TunnelRequest{Session: id, Type: kind, TargetHost: host, TargetPort: port})
		if e != nil {
			t.Fatal(kind, e)
		}
		info := v.(map[string]any)
		c, e := net.DialTimeout("tcp", info["listen_address"].(string), time.Second)
		if e != nil {
			t.Fatal(e)
		}
		c.SetDeadline(time.Now().Add(3 * time.Second))
		if kind == "dynamic" || kind == "remote_dynamic" {
			c.Write([]byte{5, 1, 0})
			var a [2]byte
			io.ReadFull(c, a[:])
			if a != [2]byte{5, 0} {
				t.Fatal(a)
			}
			c.Write([]byte{5, 1, 0, 1, 127, 0, 0, 1, byte(port >> 8), byte(port)})
			var reply [10]byte
			if _, e = io.ReadFull(c, reply[:]); e != nil || reply[1] != 0 {
				t.Fatalf("%s %v %v", kind, reply, e)
			}
		}
		c.Write([]byte("hello\x00"))
		b := make([]byte, 6)
		if _, e = io.ReadFull(c, b); e != nil || string(b) != "hello\x00" {
			t.Fatal(kind, string(b), e)
		}
		_, e = m.Tunnel(context.Background(), "close", TunnelRequest{Session: id, Handle: info["handle"].(string)})
		if e != nil {
			t.Fatal(e)
		}
		c.Close()
	}
	v, e := m.Tunnel(context.Background(), "open", TunnelRequest{Session: id, Type: "local", TargetHost: host, TargetPort: port})
	if e != nil {
		t.Fatal(e)
	}
	info := v.(map[string]any)
	m.Configure(id, cfg(map[string]any{"read_only": true}))
	if _, err := m.Tunnel(context.Background(), "open", TunnelRequest{Session: id, Type: "dynamic"}); err == nil {
		t.Fatal("read_only tunnel allowed")
	}
	m.Configure(id, cfg(map[string]any{"read_only": false}))
	session, _ := m.Get(id)
	waitUntil := time.Now().Add(time.Second)
	for {
		session.mu.Lock()
		active := session.active
		session.mu.Unlock()
		if active == 0 {
			break
		}
		if time.Now().After(waitUntil) {
			t.Fatal("tunnel operations did not drain")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := m.Reconnect(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		conn, err := net.DialTimeout("tcp", info["listen_address"].(string), 50*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("reconnect left old listener open")
		}
		time.Sleep(time.Millisecond)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	os.WriteFile(src, []byte{63, 0, 0, 0}, 0600)
	r, e := m.Transfer(context.Background(), true, FileRequest{Session: id, Src: src, Dst: dst, Backend: "sftp"})
	if e != nil || r["complete"] != true {
		t.Fatal(r, e)
	}
	again, err := m.Transfer(context.Background(), true, FileRequest{Session: id, Src: src, Dst: dst, Backend: "sftp"})
	if err == nil && again["complete"] == true {
		t.Fatal("SFTP clobbered")
	}
	if again != nil {
		if temp, ok := again["temporary_path"].(string); ok {
			os.Remove(temp)
		}
	}
	r, e = m.Transfer(context.Background(), false, FileRequest{Session: id, Src: dst, Backend: "auto"})
	if e != nil || r["complete"] != true {
		t.Fatal(r, e)
	}
	defer os.RemoveAll(filepath.Dir(r["dst"].(string)))
	b, _ := os.ReadFile(r["dst"].(string))
	if string(b) != string([]byte{63, 0, 0, 0}) {
		t.Fatal(b)
	}
}
