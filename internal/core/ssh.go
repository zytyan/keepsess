package core

import (
	"context"
	"errors"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func (s *Session) connectSSH(ctx context.Context, o Options) error {
	if o.Username == "" {
		return errors.New("SSH user is required")
	}
	var host ssh.HostKeyCallback
	switch {
	case o.Insecure:
		host = ssh.InsecureIgnoreHostKey()
	case o.HostKey != "":
		host = func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if ssh.FingerprintSHA256(k) != o.HostKey {
				return errors.New("SSH host key fingerprint mismatch")
			}
			return nil
		}
	case o.KnownHosts != "":
		var e error
		host, e = knownhosts.New(o.KnownHosts)
		if e != nil {
			return e
		}
	default:
		host = ssh.InsecureIgnoreHostKey()
	}
	auth := []ssh.AuthMethod{}
	if o.Password != "" {
		auth = append(auth, ssh.Password(o.Password))
	}
	if o.Keyfile != "" {
		b, e := os.ReadFile(o.Keyfile)
		if e != nil {
			return e
		}
		var key ssh.Signer
		if o.KeyPassphrase != "" {
			key, e = ssh.ParsePrivateKeyWithPassphrase(b, []byte(o.KeyPassphrase))
		} else {
			key, e = ssh.ParsePrivateKey(b)
		}
		if e != nil {
			return e
		}
		auth = append(auth, ssh.PublicKeys(key))
	}
	if len(auth) == 0 {
		return errors.New("SSH password or key_file required")
	}
	c, e := (&net.Dialer{Timeout: s.config.Duration(s.config.ConnectTimeoutMS), KeepAlive: s.keepalivePeriod()}).DialContext(ctx, "tcp", o.address())
	if e != nil {
		return e
	}
	s.closer = c
	s.tcp = c.(*net.TCPConn)
	if e = s.setKeepalive(s.config.TCPKeepaliveMS); e != nil {
		return e
	}
	deadline := time.Now().Add(s.config.Duration(s.config.ConnectTimeoutMS))
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	// Closing the raw socket interrupts handshake, channel setup, writes and Close.
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	cc, chans, reqs, e := ssh.NewClientConn(c, o.address(), &ssh.ClientConfig{User: o.Username, Auth: auth, HostKeyCallback: host, Timeout: IOLimit})
	if e != nil {
		return e
	}
	client := ssh.NewClient(cc, chans, reqs)
	if s.mode == "stream" {
		sh, e := client.NewSession()
		if e != nil {
			return e
		}
		sh.Stdout = s
		sh.Stderr = s
		in, e := sh.StdinPipe()
		if e != nil {
			return e
		}
		s.input = in
		if e = sh.RequestPty("vt100", 24, 80, ssh.TerminalModes{ssh.ECHO: 0, ssh.TTY_OP_ISPEED: 9600, ssh.TTY_OP_OSPEED: 9600}); e != nil {
			return e
		}
		if e = sh.Shell(); e != nil {
			return e
		}
		go func() {
			e := sh.Wait()
			why := "remote shell closed"
			if e != nil {
				why = e.Error()
			}
			var missing *ssh.ExitMissingError
			if e == nil || errors.As(e, &missing) {
				s.disconnect("")
			} else {
				s.disconnect(why)
			}
		}()
	} else {
		s.newExec = func() (execChannel, error) {
			sh, err := client.NewSession()
			if err != nil {
				return nil, err
			}
			return &sshExecChannel{Session: sh}, nil
		}
	}
	_ = c.SetDeadline(time.Time{})
	go func() {
		e := client.Wait()
		if e != nil {
			s.disconnect("SSH disconnected: " + e.Error())
		} else {
			s.disconnect("")
		}
	}()
	return nil
}

// The task owns its channel and output sink; channels share only the transport.
type sshExecChannel struct{ *ssh.Session }

func (sh *sshExecChannel) start(command string, out *Session) error {
	sh.Stdout, sh.Stderr = out, out
	return sh.Start(command)
}
