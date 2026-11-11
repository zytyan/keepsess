package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/daemon/wire"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestWireRequestPresenceAndExactValues(t *testing.T) {
	f := false
	offset := int64(1 << 60)
	r := core.Request{Session: "s1", Columns: 132, Rows: 42, Login: &f, CollectOutput: &f, Offset: &offset, Data: "\x00\xff\r\n", Env: map[string]string{"a": "x"}, Config: core.ConfigPatch{"read_total_timeout_ms": json.RawMessage("110000")}}
	// Protobuf string fields are UTF-8; raw terminal output uses bytes in Event.
	r.Data = "\x00\r\n中文"
	got := decodeRequest(encodeRequest(r))
	if !reflect.DeepEqual(r, got) {
		t.Fatalf("roundtrip: %#v", got)
	}
	now := time.Now().UTC()
	exit := 0
	result := core.Result{StartedAt: &now, ExitCode: &exit, WaitElapsedMS: &offset}
	if !reflect.DeepEqual(result, decodeResult(encodeResult(result))) {
		t.Fatal("result roundtrip")
	}
	e := core.Event{Source: "stderr", Origin: "human", Seq: 1 << 60, Data: []byte{0, 255, 13, 10}, Time: now}
	if !reflect.DeepEqual(e, decodeEvent(encodeEvent(e))) {
		t.Fatal("event roundtrip")
	}
}
func TestWireUnknownFieldsAndBaseline(t *testing.T) {
	b, e := proto.Marshal(&wire.Envelope{Type: "heartbeat", Protocol: 2})
	if e != nil {
		t.Fatal(e)
	}
	b = protowire.AppendTag(b, 1000, protowire.BytesType)
	b = protowire.AppendString(b, "future")
	var m message
	if e = decodeMessage(b, &m); e != nil || m.Type != "heartbeat" {
		t.Fatal(m, e)
	}
	if supports(nil, "exec") || !supports(capabilities, "exec") || supports(capabilities, "future") {
		t.Fatal("capability gate")
	}
}

// Frozen baseline bytes, independent of current generated serialization.
func TestFrozenBaselineHeartbeat(t *testing.T) {
	b := []byte{0x0a, 9, 'h', 'e', 'a', 'r', 't', 'b', 'e', 'a', 't', 0x10, 2}
	var m message
	if err := decodeMessage(b, &m); err != nil || m.Type != "heartbeat" || m.Protocol != 2 {
		t.Fatal(m, err)
	}
}

func TestUnsupportedCapabilityDoesNotWrite(t *testing.T) {
	p := &peer{capabilities: []string{"task_list.v1"}}
	m, e := p.call(context.Background(), "exec", core.Request{Command: "must not run"})
	if e == nil || m.Result.InputState != "not_sent" {
		t.Fatal(m, e)
	}
}

func TestWebTerminalCapabilitiesGateOldAgents(t *testing.T) {
	p := &peer{capabilities: []string{"exec.v1", "send_text.v1", "task_list.v1"}}
	for _, kind := range []string{"web_shell", "web_resize", "web_end"} {
		result, err := p.WebOperate(context.Background(), kind, core.Request{Session: "s1", Handle: "task1"})
		if err == nil || result.InputState != "not_sent" {
			t.Fatal(kind, result, err)
		}
	}
}
