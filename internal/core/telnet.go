package core

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/zytyan/keepsess/internal/telnet"
	"golang.org/x/crypto/ssh"
)

func (s *Session) connectTelnet(ctx context.Context, o Options) error {
	if len(o.SSHJumps) != 0 {
		return s.openSSHRoute(ctx, o, false, func(client *ssh.Client, _ string) error {
			raw, err := client.DialContext(ctx, "tcp", o.address())
			if err != nil {
				return routeFailure(o, len(o.SSHJumps), "forward", err)
			}
			// SSH channel connections do not implement deadlines. Bound both
			// negotiation replies and application writes by closing the shared
			// root transport when a write expires (including channel-window stalls).
			s.attachTelnet(&sshTelnetConn{Conn: raw, session: s})
			s.watchSSH(client)
			return nil
		})
	}
	raw, e := (&net.Dialer{Timeout: s.config.Duration(s.config.ConnectTimeoutMS), KeepAlive: s.keepalivePeriod()}).DialContext(ctx, "tcp", o.address())
	if e != nil {
		return e
	}
	s.closer = raw
	s.tcp = raw.(*net.TCPConn)
	if e = s.setKeepalive(s.config.TCPKeepaliveMS); e != nil {
		return e
	}
	s.attachTelnet(raw)
	return nil
}

func (s *Session) attachTelnet(raw net.Conn) {
	c := telnet.NewConn(raw, IOLimit)
	s.input = c
	go func() {
		_, e := io.Copy(s, c)
		if e == nil {
			e = io.EOF
		}
		if e == io.EOF {
			s.disconnect("")
		} else {
			s.disconnectError(e, "Telnet disconnected")
		}
	}()
}

// sshTelnetConn adapts only write deadlines used by the internal Telnet codec.
// A raw TCP deadline cannot unblock an SSH channel waiting for window credit.
type sshTelnetConn struct {
	net.Conn
	session  *Session
	mu       sync.Mutex
	deadline time.Time
}

func (c *sshTelnetConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.deadline = deadline
	c.mu.Unlock()
	return nil
}

func (c *sshTelnetConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	if deadline.IsZero() {
		return c.Conn.Write(p)
	}
	if !time.Now().Before(deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	expired := make(chan struct{})
	timer := time.AfterFunc(time.Until(deadline), func() {
		c.session.disconnectError(os.ErrDeadlineExceeded, "Telnet SSH tunnel write timeout; input outcome may be unknown")
		close(expired)
	})
	n, err := c.Conn.Write(p)
	if !timer.Stop() {
		<-expired
		err = os.ErrDeadlineExceeded
	}
	return n, err
}
