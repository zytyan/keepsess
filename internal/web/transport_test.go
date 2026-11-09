package web

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
