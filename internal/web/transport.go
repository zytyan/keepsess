package web

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/history"
)

const outputBatchBytes = 64 << 10

// Version 1 output frame: version, sequence, epoch, encoding, ID lengths, IDs,
// raw bytes. Control events remain JSON. Neither transport changes raw history.
func outputFrame(e core.Event) ([]byte, error) {
	if len(e.Session) > 255 || len(e.Handle) > 255 || len(e.Data) > outputBatchBytes {
		return nil, errors.New("invalid output frame size")
	}
	// JSON control frames already support additive event metadata.
	if e.Source != "" || e.Origin != "" {
		b, err := json.Marshal(map[string]any{"type": "event", "event": e})
		return append([]byte{0}, b...), err
	}
	encoding := byte(0)
	switch e.Encoding {
	case "", "utf-8":
	case "gbk":
		encoding = 1
	case "shift-jis":
		encoding = 2
	default:
		return nil, errors.New("unsupported output encoding")
	}
	b := make([]byte, 20+len(e.Session)+len(e.Handle)+len(e.Data))
	b[0] = 1
	binary.BigEndian.PutUint64(b[1:9], e.Seq)
	binary.BigEndian.PutUint64(b[9:17], e.Epoch)
	b[17] = encoding
	b[18] = byte(len(e.Session))
	b[19] = byte(len(e.Handle))
	n := 20 + copy(b[20:], e.Session)
	n += copy(b[n:], e.Handle)
	copy(b[n:], e.Data)
	return b, nil
}

// Timestamp segments retain original observation boundaries inside a v2 batch.
// The raw event and v1 frame contracts remain unchanged.
type outputSegment struct {
	End  int    `json:"end"`
	Seq  uint64 `json:"seq"`
	Time string `json:"time,omitempty"`
}

const maxOutputSegments = 256

type outputBatch struct {
	precise  bool
	segments []outputSegment
	event    core.Event
	emit     func([]byte) error
}

func (b *outputBatch) flush() error {
	if b.event.Kind == "" {
		return nil
	}
	var frame []byte
	var err error
	if b.precise {
		var payload []byte
		payload, err = json.Marshal(struct {
			Type     string          `json:"type"`
			Event    core.Event      `json:"event"`
			Segments []outputSegment `json:"segments"`
		}{"event", b.event, b.segments})
		frame = append([]byte{0}, payload...)
	} else {
		frame, err = outputFrame(b.event)
	}
	b.segments = nil
	b.event = core.Event{}
	if err != nil {
		return err
	}
	return b.emit(frame)
}
func (b *outputBatch) add(e core.Event) error {
	if len(e.Session) > 255 || len(e.Handle) > 255 || len(e.Data) > outputBatchBytes {
		return errors.New("invalid output frame size")
	}
	p := &b.event
	if p.Kind != "" && (p.Session != e.Session || p.Handle != e.Handle || p.Epoch != e.Epoch || p.Encoding != e.Encoding || p.Source != e.Source || p.Origin != e.Origin || len(p.Data)+len(e.Data) > outputBatchBytes || b.precise && len(b.segments) >= maxOutputSegments) {
		if err := b.flush(); err != nil {
			return err
		}
	}
	if b.event.Kind == "" {
		b.event = core.Event{Kind: "output", Session: e.Session, Handle: e.Handle, Epoch: e.Epoch, Encoding: e.Encoding, Source: e.Source, Origin: e.Origin, Time: e.Time}
	}
	b.event.Seq = e.Seq
	b.event.Data = append(b.event.Data, e.Data...)
	if b.precise {
		stamp := ""
		if !e.Time.IsZero() {
			stamp = e.Time.UTC().Format(time.RFC3339Nano)
		}
		b.segments = append(b.segments, outputSegment{End: len(b.event.Data), Seq: e.Seq, Time: stamp})
	}
	return nil
}

// replay returns a bounded snapshot, rather than an event-at-a-time animation.
// Larger archives continue at an exact cursor; clients reveal only after restore.
func (s *Server) replay(w http.ResponseWriter, r *http.Request) {
	backend, lookupErr := s.backend(r)
	if lookupErr != nil {
		fail(w, lookupErr)
		return
	}
	select {
	case s.replays <- struct{}{}:
		defer func() { <-s.replays }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "history busy; retry shortly", 503)
		return
	}
	q := r.URL.Query()
	after, err := strconv.ParseUint(defaultString(q.Get("after"), "0"), 10, 64)
	if err != nil {
		fail(w, err)
		return
	}
	if len(q.Get("handles")) > 512 {
		http.Error(w, "too many task handles", 400)
		return
	}
	handles := strings.Split(q.Get("handles"), ",")
	if len(handles) > 20 {
		http.Error(w, "too many task handles", 400)
		return
	}
	selected := map[string]bool{}
	for _, h := range handles {
		selected[h] = true
	}
	if h, ok := q["handle"]; ok {
		selected = map[string]bool{h[0]: true}
	}
	session := q.Get("session")
	var body bytes.Buffer
	body.WriteString("KSR1")
	appendFrame := func(frame []byte) error {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(frame)))
		body.Write(size[:])
		body.Write(frame)
		return nil
	}
	batch := outputBatch{precise: q.Get("format") == "binary-v2", emit: appendFrame}
	state := history.State{}
	end := time.Now().Add(5 * time.Second)
	more := false
	for scan := 0; scan < 32; scan++ {
		if r.Context().Err() != nil {
			return
		}
		page, e := backend.Store.Read(q.Get("instance"), after, 4096)
		if e != nil {
			fail(w, e)
			return
		}
		state = page.State
		for _, event := range page.Events {
			if body.Len()+len(batch.event.Data) > 6<<20 {
				more = true
				goto done
			}
			if event.Kind == "output" || event.Kind == "input" {
				if event.Session != session || ((q.Has("handle") || q.Has("handles")) && !selected[event.Handle]) {
					after = event.Seq
					continue
				}
			}
			if event.Kind == "output" {
				err = batch.add(event)
			} else {
				err = batch.flush()
				if err == nil {
					var frame []byte
					frame, err = json.Marshal(map[string]any{"type": "event", "event": event})
					if err == nil {
						err = appendFrame(append([]byte{0}, frame...))
					}
				}
			}
			if err != nil {
				fail(w, err)
				return
			}
			after = event.Seq
		}
		after = page.Next
		more = page.More
		if !more || time.Now().After(end) {
			break
		}
	}
done:
	if err = batch.flush(); err != nil {
		fail(w, err)
		return
	}
	tail, _ := json.Marshal(map[string]any{"type": "replay_end", "next": after, "more": more, "history": state, "instance_id": s.Manager.InstanceID()})
	_ = appendFrame(append([]byte{0}, tail...))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Vary", "Accept-Encoding")
	if acceptsGzip(r.Header.Get("Accept-Encoding")) {
		w.Header().Set("Content-Encoding", "gzip")
		z, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
		_, _ = z.Write(body.Bytes())
		_ = z.Close()
	} else {
		_, _ = w.Write(body.Bytes())
	}
}
func acceptsGzip(header string) bool {
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		if parts[0] != "gzip" {
			continue
		}
		for _, p := range parts[1:] {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "q=") {
				v, err := strconv.ParseFloat(strings.TrimPrefix(p, "q="), 64)
				return err == nil && v > 0
			}
		}
		return true
	}
	return false
}

// EncodeOutput and DecodeOutput also serve the authenticated local relay.
func EncodeOutput(e core.Event) ([]byte, error) { return outputFrame(e) }
func DecodeOutput(b []byte) (core.Event, error) {
	if len(b) > 0 && b[0] == 0 {
		var v struct {
			Type  string     `json:"type"`
			Event core.Event `json:"event"`
		}
		err := json.Unmarshal(b[1:], &v)
		if err != nil || v.Type != "event" || v.Event.Kind != "output" || len(v.Event.Data) > outputBatchBytes {
			return core.Event{}, errors.New("invalid output frame")
		}
		return v.Event, nil
	}
	if len(b) < 20 || b[0] != 1 || b[17] > 2 || len(b) > outputBatchBytes+530 {
		return core.Event{}, errors.New("invalid output frame")
	}
	n := 20 + int(b[18])
	end := n + int(b[19])
	if end > len(b) {
		return core.Event{}, errors.New("incomplete output frame")
	}
	return core.Event{Kind: "output", Seq: binary.BigEndian.Uint64(b[1:9]), Epoch: binary.BigEndian.Uint64(b[9:17]), Encoding: []string{"utf-8", "gbk", "shift-jis"}[b[17]], Session: string(b[20:n]), Handle: string(b[n:end]), Data: append([]byte(nil), b[end:]...)}, nil
}
