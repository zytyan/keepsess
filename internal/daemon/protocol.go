package daemon

import (
	"errors"

	"github.com/gorilla/websocket"
	"github.com/zytyan/keepsess/internal/daemon/wire"
	"google.golang.org/protobuf/proto"
)

// Baseline capabilities are permanent contracts, independent of build versions.
var capabilities = []string{"exec.v1", "send_text.v1", "task_list.v1"}

func supports(caps []string, kind string) bool {
	for _, c := range caps {
		if c == kind+".v1" {
			return true
		}
	}
	return false
}
func writeMessage(c *websocket.Conn, m message) error {
	w := &wire.Envelope{Type: m.Type, Protocol: uint32(m.Protocol), Instance: m.Instance, Pid: int64(m.PID), Id: m.ID, Kind: m.Kind, Request: encodeRequest(m.Request), History: encodeHistory(m.History), Result: encodeResult(m.Result), Error: m.Error, Capabilities: m.Capabilities}
	if m.Event != nil {
		w.Event = encodeEvent(*m.Event)
	}
	for _, r := range m.Tasks {
		w.Tasks = append(w.Tasks, encodeResult(r))
	}
	b, err := proto.Marshal(w)
	if err != nil {
		return err
	}
	return c.WriteMessage(websocket.BinaryMessage, b)
}
func decodeMessage(b []byte, m *message) error {
	var w wire.Envelope
	if err := proto.Unmarshal(b, &w); err != nil {
		return err
	}
	*m = message{Type: w.Type, Protocol: int(w.Protocol), Instance: w.Instance, PID: int(w.Pid), ID: w.Id, Kind: w.Kind, Request: decodeRequest(w.Request), History: decodeHistory(w.History), Result: decodeResult(w.Result), Error: w.Error, Capabilities: w.Capabilities}
	if w.Event != nil {
		e := decodeEvent(w.Event)
		m.Event = &e
	}
	for _, r := range w.Tasks {
		m.Tasks = append(m.Tasks, decodeResult(r))
	}
	return nil
}
func readMessage(c *websocket.Conn, m *message) error {
	kind, b, err := c.ReadMessage()
	if err != nil {
		return err
	}
	if kind != websocket.BinaryMessage {
		return errors.New("incompatible daemon protocol; restart the Agent when its remote sessions can be closed")
	}
	return decodeMessage(b, m)
}
