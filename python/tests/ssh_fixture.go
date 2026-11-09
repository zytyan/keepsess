// Local protocol fixture for the Python SDK end-to-end tests, never installed.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		panic(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if string(password) != "sdk-fixture" {
			return nil, fmt.Errorf("bad fixture password")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	fmt.Println(listener.Addr().String())
	for {
		conn, err := listener.Accept()
		if err != nil {
			os.Exit(1)
		}
		go func() {
			server, channels, requests, err := ssh.NewServerConn(conn, config)
			if err != nil {
				conn.Close()
				return
			}
			defer server.Close()
			go ssh.DiscardRequests(requests)
			for incoming := range channels {
				if incoming.ChannelType() != "session" {
					incoming.Reject(ssh.UnknownChannelType, "session only")
					continue
				}
				ch, requests, err := incoming.Accept()
				if err != nil {
					continue
				}
				go func() {
					defer ch.Close()
					for r := range requests {
						if r.Type != "exec" {
							r.Reply(false, nil)
							continue
						}
						var payload struct{ Command string }
						ssh.Unmarshal(r.Payload, &payload)
						r.Reply(true, nil)
						if strings.Contains(payload.Command, "SDK_SLOW") {
							time.Sleep(150 * time.Millisecond)
						}
						if strings.Contains(payload.Command, "SDK_INTERACTIVE") {
							data, _ := io.ReadAll(ch)
							ch.Write(data)
						} else {
							ch.Write([]byte("stdout-中文\n"))
							ch.Stderr().Write([]byte("stderr-fixture\n"))
						}
						if !strings.Contains(payload.Command, "SDK_UNKNOWN") {
							status := make([]byte, 4)
							binary.BigEndian.PutUint32(status, 7)
							ch.SendRequest("exit-status", false, status)
						}
						return
					}
				}()
			}
		}()
	}
}
