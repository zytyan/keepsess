package core

import (
	"context"
	"io"
	"net"

	"github.com/zytyan/keepsess/internal/telnet"
)

func (s *Session) connectTelnet(ctx context.Context, o Options) error {
	raw, e := (&net.Dialer{Timeout: s.config.Duration(s.config.ConnectTimeoutMS), KeepAlive: s.keepalivePeriod()}).DialContext(ctx, "tcp", o.address())
	if e != nil {
		return e
	}
	s.closer = raw
	s.tcp = raw.(*net.TCPConn)
	if e = s.setKeepalive(s.config.TCPKeepaliveMS); e != nil {
		return e
	}
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
			s.disconnect("Telnet disconnected: " + e.Error())
		}
	}()
	return nil
}
