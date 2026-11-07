package core

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type TunnelRequest struct {
	Session    string `json:"session"`
	Handle     string `json:"handle"`
	Type       string `json:"type"`
	BindHost   string `json:"bind_host"`
	BindPort   int    `json:"bind_port"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
}

var tunnelCount atomic.Int32

type Tunnel struct {
	done        chan struct{}
	transport   io.Closer
	mu          sync.Mutex
	listener    net.Listener
	connections map[net.Conn]bool
	closed      bool
	info        map[string]any
}

func (t *Tunnel) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	close(t.done)
	tunnelCount.Add(-1)
	timer := time.AfterFunc(time.Second, func() { t.transport.Close() })
	t.listener.Close()
	defer timer.Stop()
	for c := range t.connections {
		c.Close()
	}
}
func (t *Tunnel) track(c net.Conn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		c.Close()
		return false
	}
	t.connections[c] = true
	return true
}
func (t *Tunnel) forget(c net.Conn) {
	t.mu.Lock()
	delete(t.connections, c)
	t.mu.Unlock()
	timer := time.AfterFunc(time.Second, func() { t.transport.Close() })
	defer timer.Stop()
	c.Close()
}
func (m *Manager) Tunnel(ctx context.Context, action string, r TunnelRequest) (any, error) {
	s, e := m.Get(r.Session)
	if e != nil {
		return nil, e
	}
	if action != "open" {
		s.mu.Lock()
		defer s.mu.Unlock()
		if action == "list" {
			a := []any{}
			for _, t := range s.tunnels {
				t.mu.Lock()
				info := map[string]any{}
				for k, v := range t.info {
					info[k] = v
				}
				info["closed"] = t.closed
				info["active_sockets"] = len(t.connections)
				t.mu.Unlock()
				a = append(a, info)
			}
			return a, nil
		}
		t := s.tunnels[r.Handle]
		if t == nil {
			return nil, errors.New("unknown tunnel handle")
		}
		t.close()
		delete(s.tunnels, r.Handle)
		return map[string]any{"handle": r.Handle, "closed": true}, nil
	}
	if r.Type != "local" && r.Type != "remote" && r.Type != "dynamic" && r.Type != "remote_dynamic" {
		return nil, errors.New("type must be local/remote/dynamic/remote_dynamic")
	}
	dynamic := r.Type == "dynamic" || r.Type == "remote_dynamic"
	remote := r.Type == "remote" || r.Type == "remote_dynamic"
	if r.BindPort < 0 || r.BindPort > 65535 || (!dynamic && (r.TargetHost == "" || r.TargetPort < 1 || r.TargetPort > 65535)) {
		return nil, errors.New("invalid bind or target address")
	}
	if r.BindHost == "" {
		r.BindHost = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, c, done, e := m.auxiliary(ctx, r.Session, true)
	if e != nil {
		return nil, e
	}
	defer done()
	if !s.configMu.TryLock() {
		return nil, errors.New("busy: configure or tunnel open in progress")
	}
	defer s.configMu.Unlock()
	s.mu.Lock()
	if len(s.tunnels) >= 16 {
		s.mu.Unlock()
		return nil, errors.New("tunnel limit 16 per session")
	}
	s.mu.Unlock()
	if tunnelCount.Add(1) > 64 {
		tunnelCount.Add(-1)
		return nil, errors.New("global tunnel limit 64")
	}
	reserved := true
	defer func() {
		if reserved {
			tunnelCount.Add(-1)
		}
	}()
	address := net.JoinHostPort(r.BindHost, strconv.Itoa(r.BindPort))
	var l net.Listener
	if remote {
		l, e = c.Listen("tcp", address)
	} else {
		l, e = net.Listen("tcp", address)
	}
	if e != nil {
		return nil, e
	}
	reserved = false
	t := &Tunnel{listener: l, connections: map[net.Conn]bool{}, done: make(chan struct{}), transport: c}
	s.mu.Lock()
	if s.tunnels == nil {
		s.tunnels = map[string]*Tunnel{}
	}
	s.nextTunnel++
	id := fmt.Sprintf("%s-tunnel%d", s.id, s.nextTunnel)
	t.info = map[string]any{"session": s.id, "handle": id, "type": r.Type, "listen_address": l.Addr().String(), "target_host": r.TargetHost, "target_port": r.TargetPort}
	s.tunnels[id] = t
	s.mu.Unlock()
	s.mu.Lock()
	disconnected := s.done
	s.mu.Unlock()
	go func() {
		select {
		case <-disconnected:
			t.close()
		case <-t.done:
		}
	}()
	go func() {
		for {
			a, err := l.Accept()
			if err != nil {
				return
			}
			t.mu.Lock()
			full := len(t.connections) >= 64
			t.mu.Unlock()
			if full {
				a.Close()
				continue
			}
			if !t.track(a) {
				return
			}
			go func() {
				defer t.forget(a)
				s.touch(1)
				defer s.touch(-1)
				stageTimer := time.AfterFunc(15*time.Second, func() { c.Close() })
				defer stageTimer.Stop()
				a.SetDeadline(time.Now().Add(15 * time.Second))
				target := net.JoinHostPort(r.TargetHost, strconv.Itoa(r.TargetPort))
				if dynamic {
					var err error
					target, err = socksTarget(a)
					if err != nil {
						return
					}
				}
				dialCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				var b net.Conn
				var err error
				if remote {
					b, err = (&net.Dialer{}).DialContext(dialCtx, "tcp", target)
				} else {
					abort := context.AfterFunc(dialCtx, func() { c.Close() })
					b, err = c.DialContext(dialCtx, "tcp", target)
					abort()
				}
				if err != nil {
					if dynamic {
						a.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
					}
					return
				}
				if !t.track(b) {
					return
				}
				defer t.forget(b)
				if dynamic {
					if _, err = a.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
						return
					}
				}
				stageTimer.Stop()
				a.SetDeadline(time.Time{})
				// Bound stalled copies, retaining TCP/SSH half-close semantics.
				copySide := func(dst, src net.Conn) {
					buf := make([]byte, 32768)
					for {
						readTimer := time.AfterFunc(2*time.Hour, func() { c.Close() })
						src.SetReadDeadline(time.Now().Add(2 * time.Hour))
						n, err := src.Read(buf)
						readTimer.Stop()
						if n > 0 {
							dst.SetWriteDeadline(time.Now().Add(15 * time.Second))
							abortWrite := time.AfterFunc(15*time.Second, func() { c.Close() })
							written, werr := dst.Write(buf[:n])
							abortWrite.Stop()
							if werr != nil || written != n {
								break
							}
						}
						if err != nil {
							break
						}
					}
					if cw, ok := dst.(interface{ CloseWrite() error }); ok {
						cw.CloseWrite()
					}
				}
				ch := make(chan struct{})
				go func() { copySide(b, a); close(ch) }()
				copySide(a, b)
				a.Close()
				b.Close()
				<-ch
			}()
		}
	}()
	return t.info, nil
}
func socksTarget(c net.Conn) (string, error) {
	var h [2]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return "", e
	}
	if h[0] != 5 || h[1] == 0 {
		return "", errors.New("SOCKS5 required")
	}
	methods := make([]byte, int(h[1]))
	if _, e := io.ReadFull(c, methods); e != nil {
		return "", e
	}
	ok := false
	for _, v := range methods {
		ok = ok || v == 0
	}
	if !ok {
		c.Write([]byte{5, 255})
		return "", errors.New("SOCKS requires no-auth")
	}
	if _, e := c.Write([]byte{5, 0}); e != nil {
		return "", e
	}
	var req [4]byte
	if _, e := io.ReadFull(c, req[:]); e != nil {
		return "", e
	}
	if req[0] != 5 || req[1] != 1 || req[2] != 0 {
		return "", errors.New("only SOCKS5 CONNECT supported")
	}
	var host string
	switch req[3] {
	case 1, 4:
		n := 4
		if req[3] == 4 {
			n = 16
		}
		b := make([]byte, n)
		if _, e := io.ReadFull(c, b); e != nil {
			return "", e
		}
		host = net.IP(b).String()
	case 3:
		var b [1]byte
		if _, e := io.ReadFull(c, b[:]); e != nil {
			return "", e
		}
		name := make([]byte, int(b[0]))
		if len(name) == 0 {
			return "", errors.New("empty SOCKS host")
		}
		if _, e := io.ReadFull(c, name); e != nil {
			return "", e
		}
		host = string(name)
	default:
		return "", errors.New("invalid SOCKS address")
	}
	var port [2]byte
	if _, e := io.ReadFull(c, port[:]); e != nil {
		return "", e
	}
	p := binary.BigEndian.Uint16(port[:])
	if p == 0 {
		return "", errors.New("invalid port")
	}
	return net.JoinHostPort(host, strconv.Itoa(int(p))), nil
}
