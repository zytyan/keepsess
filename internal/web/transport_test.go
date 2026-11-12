package web

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/history"
)

func waitRecorded(t *testing.T, st *history.Store) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for st.Status().WrittenSeq < st.Status().LastSeq && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if s := st.Status(); s.Error != "" || s.WrittenSeq < s.LastSeq {
		t.Fatal(s)
	}
}
func decodeOutput(t *testing.T, b []byte) core.Event {
	t.Helper()
	if len(b) < 20 || b[0] != 1 || b[17] > 2 || 20+int(b[18])+int(b[19]) > len(b) {
		t.Fatal("bad frame")
	}
	n := 20 + int(b[18])
	end := n + int(b[19])
	return core.Event{Kind: "output", Seq: binary.BigEndian.Uint64(b[1:9]), Epoch: binary.BigEndian.Uint64(b[9:17]), Encoding: []string{"utf-8", "gbk", "shift-jis"}[b[17]], Session: string(b[20:n]), Handle: string(b[n:end]), Data: b[end:]}
}
func TestReplayRawFilteringGzipAndFontCache(t *testing.T) {
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0", BasePath: "/prefix/"})
	var want []byte
	st.Emit(core.Event{Kind: "command", Session: "s1", Handle: "task1", Command: "sample"})
	for i := 0; i < 600; i++ {
		data := []byte(fmt.Sprintf("row %04d 中文\x1b[32m\x00\xff\r\n", i))
		want = append(want, data...)
		st.Emit(core.Event{Kind: "output", Session: "s1", Handle: "task1", Epoch: 3, Encoding: "utf-8", Data: data})
		if i%50 == 0 {
			waitRecorded(t, st)
		}
	}
	st.Emit(core.Event{Kind: "output", Session: "s1", Handle: "task2", Data: []byte("not this task")})
	st.Emit(core.Event{Kind: "session", Session: "s2", Name: "global-metadata"})
	waitRecorded(t, st)
	req, _ := http.NewRequest("GET", s.Info().URL+"api/replay?instance="+m.InstanceID()+"&session=s1&handle=task1", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 || r.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal(r.Status, r.Header)
	}
	z, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	z.Close()
	if string(b[:4]) != "KSR1" {
		t.Fatal("magic")
	}
	var got []byte
	frames := 0
	metadata := false
	ended := false
	for i := 4; i < len(b); {
		n := int(binary.BigEndian.Uint32(b[i:]))
		i += 4
		frame := b[i : i+n]
		i += n
		if frame[0] == 1 {
			e := decodeOutput(t, frame)
			if e.Handle != "task1" || e.Epoch != 3 {
				t.Fatal(e)
			}
			got = append(got, e.Data...)
			frames++
		} else {
			var msg struct {
				Type  string     `json:"type"`
				Event core.Event `json:"event"`
				Next  uint64     `json:"next"`
				More  bool       `json:"more"`
			}
			if err = json.Unmarshal(frame[1:], &msg); err != nil {
				t.Fatal(err)
			}
			if msg.Event.Name == "global-metadata" {
				metadata = true
			}
			if msg.Type == "replay_end" {
				ended = true
				if msg.More || msg.Next != st.Status().LastSeq {
					t.Fatal(msg)
				}
			}
		}
	}
	if !bytes.Equal(got, want) || frames != 1 || !metadata || !ended {
		t.Fatal("snapshot mismatch", len(got), len(want), frames, metadata, ended)
	}
	for _, item := range []struct {
		path  string
		cache bool
	}{{"fonts/JetBrainsMono-662a196d58f1.ttf", true}, {"fonts/NotoSansSC-2057fd5e1572.woff2", true}, {"api/info", false}} {
		r, err := http.Get(s.Info().URL + item.path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if item.cache {
			if r.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" || r.Header.Get("ETag") == "" {
				t.Fatal(r.Header)
			}
		} else if r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(r.Header)
		}
	}
	t.Logf("600 output records restored in %d binary frame (%d raw bytes)", frames, len(got))
}
func TestLiveBinaryBatchOrderAndReadonly(t *testing.T) {
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0"})
	waitRecorded(t, st)
	u := "ws" + s.Info().URL[4:] + "ws?session=s1&handle=task1&format=binary-v1"
	c, _, err := websocket.DefaultDialer.Dial(u, http.Header{"Origin": []string{s.Info().URL[:len(s.Info().URL)-1]}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	readType(t, c, "hello")
	var want []byte
	legacyBytes := 0
	for i := 0; i < 80; i++ {
		data := []byte{byte(i), 0xff, 0x1b, '[', 'm'}
		want = append(want, data...)
		e := core.Event{Kind: "output", Session: "s1", Handle: "task1", Encoding: "gbk", Data: data, Status: core.Result{Connected: true}}
		b, _ := json.Marshal(map[string]any{"type": "event", "event": e})
		legacyBytes += len(b)
		st.Emit(e)
	}
	st.Emit(core.Event{Kind: "task", Session: "s1", Handle: "task1", Status: core.Result{State: "finished"}})
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var got []byte
	frames := 0
	binaryBytes := 0
	for {
		typ, b, err := c.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if typ == websocket.BinaryMessage {
			e := decodeOutput(t, b)
			if e.Encoding != "gbk" {
				t.Fatal(e)
			}
			got = append(got, e.Data...)
			frames++
			binaryBytes += len(b)
			continue
		}
		var msg struct {
			Type  string     `json:"type"`
			Event core.Event `json:"event"`
		}
		_ = json.Unmarshal(b, &msg)
		if msg.Event.Kind == "task" {
			break
		}
	}
	if !bytes.Equal(got, want) || frames > 4 {
		t.Fatal("lost/reordered/unbatched output", frames, len(got))
	}
	_ = c.WriteJSON(request{Type: "exec", Session: "s1", Command: "should not run"})
	reply := readType(t, c, "receipt")
	if !bytes.Contains(reply["error"], []byte("read-only")) {
		t.Fatal(reply)
	}
	if len(m.List()) != 0 {
		t.Fatal("readonly wrote")
	}
	t.Logf("80 small output records: JSON %d bytes; binary %d bytes in %d frame(s), before compression", legacyBytes, binaryBytes, frames)
}

func TestReplayLargeArchiveContinuesWithoutGaps(t *testing.T) {
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0"})
	const records = 240
	for i := 1; i <= records; i++ {
		st.Emit(core.Event{Kind: "output", Session: "s1", Handle: "task1", Encoding: "utf-8", Data: bytes.Repeat([]byte{byte(i % 251)}, 48<<10)})
		if i%8 == 0 {
			waitRecorded(t, st)
		}
	}
	waitRecorded(t, st)
	after := uint64(0)
	seen := uint64(0)
	requests := 0
	for {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%sapi/replay?instance=%s&session=s1&handle=task1&after=%d", s.Info().URL, m.InstanceID(), after), nil)
		req.Header.Set("Accept-Encoding", "identity")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatal(response.Status, err)
		}
		if len(b) > 8<<20 || len(b) < 4 || string(b[:4]) != "KSR1" {
			t.Fatal("unbounded or invalid snapshot", len(b))
		}
		requests++
		more := false
		for i := 4; i < len(b); {
			n := int(binary.BigEndian.Uint32(b[i:]))
			i += 4
			frame := b[i : i+n]
			i += n
			if frame[0] == 1 {
				event := decodeOutput(t, frame)
				seen++
				if event.Seq != seen || len(event.Data) != 48<<10 || bytes.Count(event.Data, []byte{byte(seen % 251)}) != len(event.Data) {
					t.Fatal("gap, duplication or changed bytes", event.Seq, seen)
				}
			} else {
				var end struct {
					Type string `json:"type"`
					Next uint64 `json:"next"`
					More bool   `json:"more"`
				}
				if err = json.Unmarshal(frame[1:], &end); err != nil {
					t.Fatal(err)
				}
				if end.Type == "replay_end" {
					if end.Next <= after {
						t.Fatal("cursor stalled")
					}
					after = end.Next
					more = end.More
				}
			}
		}
		if !more {
			break
		}
		if requests > 5 {
			t.Fatal("unbounded continuation")
		}
	}
	if requests < 2 || seen != records || after != records {
		t.Fatal(requests, seen, after)
	}
}

// Omitting handle selects a complete session, including tasks no longer in the
// core catalog. An explicit empty handle continues to select stream output.
func TestReplaySessionAndExplicitHandleSelection(t *testing.T) {
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0"})
	for _, e := range []core.Event{
		{Session: "s1", Data: []byte("stream")},
		{Session: "s1", Handle: "task1", Data: []byte("one")},
		{Session: "s1", Handle: "task2", Data: []byte("two")},
		{Session: "s2", Handle: "task3", Data: []byte("other")},
	} {
		e.Kind = "output"
		e.Encoding = "utf-8"
		st.Emit(e)
	}
	waitRecorded(t, st)
	for _, tc := range []struct{ query, want string }{
		{"", "streamonetwo"}, {"&handle=", "stream"},
		{"&handle=task1", "one"}, {"&handles=task1,task2", "onetwo"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			r, err := http.Get(s.Info().URL + "api/replay?instance=" + m.InstanceID() + "&session=s1" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if r.StatusCode != 200 || len(b) < 4 || string(b[:4]) != "KSR1" {
				t.Fatalf("bad replay: %s %q", r.Status, b)
			}
			var got []byte
			for i := 4; i < len(b); {
				n := int(binary.BigEndian.Uint32(b[i:]))
				i += 4
				frame := b[i : i+n]
				i += n
				if frame[0] == 1 {
					got = append(got, decodeOutput(t, frame).Data...)
				}
			}
			if string(got) != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestOutputSourcesSurviveBatchingAndFrames(t *testing.T) {
	var got []core.Event
	batch := outputBatch{emit: func(b []byte) error {
		e, err := DecodeOutput(b)
		if err == nil {
			got = append(got, e)
		}
		return err
	}}
	for i, e := range []core.Event{
		{Kind: "output", Session: "s1", Handle: "task1", Source: "stdout", Origin: "human", Data: []byte{0xe4, 0xb8}},
		{Kind: "output", Session: "s1", Handle: "task1", Source: "stderr", Origin: "human", Data: []byte("error\n")},
		{Kind: "output", Session: "s1", Handle: "task1", Source: "stdout", Origin: "human", Data: []byte{0xad, 0x0a}},
	} {
		e.Seq = uint64(i + 1)
		if err := batch.add(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.flush(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Source != "stdout" || got[1].Source != "stderr" || got[2].Source != "stdout" || got[1].Origin != "human" || !bytes.Equal(got[0].Data, []byte{0xe4, 0xb8}) {
		t.Fatal(got)
	}
	legacy := core.Event{Kind: "output", Session: "s1", Handle: "task1", Data: []byte("old mixed output")}
	b, err := EncodeOutput(legacy)
	if err != nil || b[0] != 1 {
		t.Fatal(err)
	}
	roundtrip, err := DecodeOutput(b)
	if err != nil || roundtrip.Source != "" || string(roundtrip.Data) != string(legacy.Data) {
		t.Fatal(roundtrip, err)
	}
}

func TestPreciseOutputTimestampSegments(t *testing.T) {
	stamp := time.Date(2026, 11, 12, 0, 0, 0, 123456789, time.UTC)
	var frames [][]byte
	batch := outputBatch{precise: true, emit: func(b []byte) error { frames = append(frames, b); return nil }}
	pieces := [][]byte{{0xe4, 0xb8}, {0xad, 10}, []byte("next\n")}
	for i, data := range pieces {
		if err := batch.add(core.Event{Kind: "output", Session: "s1", Handle: "task1", Seq: uint64(i + 1), Time: stamp.Add(time.Duration(i) * time.Millisecond), Encoding: "utf-8", Source: "stdout", Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.flush(); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || frames[0][0] != 0 {
		t.Fatal("expected negotiated JSON metadata frame")
	}
	var payload struct {
		Type     string          `json:"type"`
		Event    core.Event      `json:"event"`
		Segments []outputSegment `json:"segments"`
	}
	if err := json.Unmarshal(frames[0][1:], &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Segments) != 3 || payload.Event.Seq != 3 || !payload.Event.Time.Equal(stamp) || !bytes.Equal(payload.Event.Data, bytes.Join(pieces, nil)) {
		t.Fatal(payload)
	}
	offset := 0
	for i, seg := range payload.Segments {
		offset += len(pieces[i])
		if seg.End != offset || seg.Seq != uint64(i+1) || seg.Time != stamp.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339Nano) {
			t.Fatal(seg)
		}
	}
	legacy, err := EncodeOutput(core.Event{Kind: "output", Session: "s1", Seq: 1, Time: stamp, Data: []byte("legacy")})
	if err != nil || legacy[0] != 1 {
		t.Fatal("legacy frame contract changed", err)
	}
	decoded, err := DecodeOutput(legacy)
	if err != nil || !decoded.Time.IsZero() {
		t.Fatal("legacy must not invent timestamps", decoded, err)
	}
}

func TestPreciseOutputSegmentBudget(t *testing.T) {
	counts := []int{}
	batch := outputBatch{precise: true, emit: func(b []byte) error {
		var payload struct {
			Segments []outputSegment `json:"segments"`
		}
		err := json.Unmarshal(b[1:], &payload)
		counts = append(counts, len(payload.Segments))
		return err
	}}
	for i := 0; i < maxOutputSegments+1; i++ {
		if err := batch.add(core.Event{Kind: "output", Session: "s1", Seq: uint64(i + 1), Data: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.flush(); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 || counts[0] != maxOutputSegments || counts[1] != 1 {
		t.Fatal(counts)
	}
	if err := batch.add(core.Event{Session: "s1", Data: make([]byte, outputBatchBytes+1)}); err == nil {
		t.Fatal("oversized precise batch accepted")
	}
}

func TestPreciseReplayAndLiveNegotiation(t *testing.T) {
	s, m, st := setup(t, Options{Listen: "127.0.0.1:0"})
	stamp := time.Date(2026, 11, 12, 0, 0, 0, 1, time.UTC)
	st.Emit(core.Event{Kind: "session", Session: "s1", Mode: "exec", Protocol: "ssh"})
	st.Emit(core.Event{Kind: "output", Session: "s1", Handle: "task1", Time: stamp, Encoding: "utf-8", Data: []byte("one\n")})
	st.Emit(core.Event{Kind: "output", Session: "s1", Handle: "task1", Time: stamp.Add(time.Millisecond), Encoding: "utf-8", Data: []byte("two\n")})
	waitRecorded(t, st)
	check := func(frame []byte) bool {
		if len(frame) == 0 || frame[0] != 0 {
			return false
		}
		var p struct {
			Event    core.Event      `json:"event"`
			Segments []outputSegment `json:"segments"`
		}
		if err := json.Unmarshal(frame[1:], &p); err != nil {
			t.Fatal(err)
		}
		if p.Event.Kind != "output" {
			return false
		}
		if len(p.Segments) != 2 || p.Segments[0].Time != stamp.Format(time.RFC3339Nano) || p.Segments[1].Time != stamp.Add(time.Millisecond).Format(time.RFC3339Nano) || string(p.Event.Data) != "one\ntwo\n" {
			t.Fatal(p)
		}
		return true
	}
	r, err := http.Get(s.Info().URL + "api/replay?instance=" + m.InstanceID() + "&session=s1&format=binary-v2")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil || r.StatusCode != 200 {
		t.Fatal(err, r.Status)
	}
	found := false
	for i := 4; i < len(body); {
		n := int(binary.BigEndian.Uint32(body[i:]))
		i += 4
		if check(body[i : i+n]) {
			found = true
		}
		i += n
	}
	if !found {
		t.Fatal("negotiated replay timestamps missing")
	}
	u, err := url.Parse(s.Info().URL)
	if err != nil {
		t.Fatal(err)
	}
	origin := u.Scheme + "://" + u.Host
	u.Scheme = "ws"
	u.Path += "ws"
	u.RawQuery = "session=s1&format=binary-v2"
	ws, _, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Origin": []string{origin}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 20; i++ {
		kind, frame, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.BinaryMessage && check(frame) {
			return
		}
	}
	t.Fatal("negotiated live timestamps missing")
}
