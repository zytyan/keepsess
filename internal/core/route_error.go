package core

import "fmt"

type RouteError struct {
	HopIndex int    `json:"hop_index"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	FromHost string `json:"from_host"`
	Stage    string `json:"stage"`
	Target   bool   `json:"target"`
	label    string
	err      error
}

func (e *RouteError) Error() string {
	return fmt.Sprintf("%s: hop_index=%d host=%s port=%d from_host=%s stage=%s: %v; order: local -> ssh_jumps[0] -> ssh_jumps[1] -> ... -> target", e.label, e.HopIndex, e.Host, e.Port, e.FromHost, e.Stage, e.err)
}
func (e *RouteError) Unwrap() error { return e.err }

func routeFailure(o Options, index int, stage string, err error) error {
	endpoint := o
	target := index == len(o.SSHJumps)
	label := "SSH target"
	if target && o.Protocol == "telnet" {
		label = "Telnet target"
	}
	if !target {
		endpoint = o.SSHJumps[index].options()
		label = fmt.Sprintf("SSH jump %d", index+1)
	}
	from := "local"
	if index > 0 {
		from = o.SSHJumps[index-1].Host
		if stage == "forward" {
			label = fmt.Sprintf("SSH jump %d forwarding to %s", index, label)
		}
	}
	return &RouteError{HopIndex: index + 1, Host: endpoint.Host, Port: endpoint.Port, FromHost: from, Stage: stage, Target: target, label: label, err: err}
}
