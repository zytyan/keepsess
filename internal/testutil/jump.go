package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
)

type JumpStats struct {
	Active          atomic.Int32
	Forwards        atomic.Int32
	Sessions        atomic.Int32
	Probes          atomic.Int32
	IgnoreKeepalive atomic.Bool
}

// SSHJump only forwards to the allowed next endpoint. stall deliberately never
// answers a channel-open request, exercising cancellation of a half-open hop.
func SSHJump(t *testing.T, destination string, stall bool) (string, string, *JumpStats) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if c.User() != "jump-user" || string(password) != "jump-secret" {
			return nil, errors.New("bad jump authentication")
		}
		return nil, nil
	}, PublicKeyCallback: func(c ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) {
		if c.User() != "jump-user" {
			return nil, errors.New("bad jump user")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	stats := &JumpStats{}
	addr := Listen(t, func(raw net.Conn) {
		stats.Active.Add(1)
		defer stats.Active.Add(-1)
		sc, channels, requests, err := ssh.NewServerConn(raw, config)
		if err != nil {
			return
		}
		defer sc.Close()
		go func() {
			for req := range requests {
				if req.Type == "keepalive@openssh.com" {
					stats.Probes.Add(1)
					if stats.IgnoreKeepalive.Load() {
						continue
					}
				}
				_ = req.Reply(false, nil)
			}
		}()
		var wg sync.WaitGroup
		defer wg.Wait()
		for nc := range channels {
			if nc.ChannelType() != "direct-tcpip" {
				stats.Sessions.Add(1)
				_ = nc.Reject(ssh.UnknownChannelType, "forwarding only")
				continue
			}
			stats.Forwards.Add(1)
			if stall {
				continue
			}
			var request struct {
				Host       string
				Port       uint32
				OriginHost string
				OriginPort uint32
			}
			if ssh.Unmarshal(nc.ExtraData(), &request) != nil || net.JoinHostPort(request.Host, fmt.Sprint(request.Port)) != destination {
				_ = nc.Reject(ssh.Prohibited, "unexpected next hop")
				continue
			}
			target, err := net.Dial("tcp", destination)
			if err != nil {
				_ = nc.Reject(ssh.ConnectionFailed, "target unavailable")
				continue
			}
			channel, reqs, err := nc.Accept()
			if err != nil {
				_ = target.Close()
				return
			}
			go ssh.DiscardRequests(reqs)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer target.Close()
				defer channel.Close()
				copied := make(chan struct{})
				go func() {
					_, _ = io.Copy(target, channel)
					_ = target.(*net.TCPConn).CloseWrite()
					close(copied)
				}()
				_, _ = io.Copy(channel, target)
				_ = channel.Close()
				_ = target.Close()
				<-copied
			}()
		}
	})
	return addr, ssh.FingerprintSHA256(signer.PublicKey()), stats
}
