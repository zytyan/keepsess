//go:build linux

package core

import (
	"golang.org/x/sys/unix"
	"net"
)

func queueSupported() bool { return true }
func queueMetric() string {
	return "linux_siocoutq: TCP bytes queued including unacknowledged bytes; entire encrypted transport for SSH"
}
func queuedBytes(c *net.TCPConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var n int
	var inner error
	err = raw.Control(func(fd uintptr) { n, inner = unix.IoctlGetInt(int(fd), unix.TIOCOUTQ) })
	if err != nil {
		return 0, err
	}
	return n, inner
}
