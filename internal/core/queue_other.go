//go:build !linux

package core

import (
	"errors"
	"net"
)

func queueSupported() bool                    { return false }
func queueMetric() string                     { return "unsupported; bounded Write still applies" }
func queuedBytes(c *net.TCPConn) (int, error) { return 0, errors.New("OS TCP queue query unsupported") }
