package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/testutil"
)

func manager(t *testing.T) *Manager { t.Helper(); m := NewManager(); t.Cleanup(m.CloseAll); return m }
func patch(v map[string]any) ConfigPatch {
	b, _ := json.Marshal(v)
	var p ConfigPatch
	_ = json.Unmarshal(b, &p)
	return p
}
func cfg(v map[string]any) ConfigPatch { return patch(v) }
func options(addr, protocol, mode string) Options {
	host, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	return Options{Protocol: protocol, Host: host, Port: n, Mode: mode, Username: "test", Password: "test"}
}
func connect(t *testing.T, m *Manager, o Options) string {
	t.Helper()
	v, err := m.Connect(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return v.Session
}
func run(t *testing.T, m *Manager, kind string, r Request) Result {
	t.Helper()
	v, err := m.Operate(context.Background(), kind, r)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func noCollect() *bool { b := false; return &b }
func localSession(t *testing.T) (*Manager, *Session, *bytes.Buffer) {
	t.Helper()
	m := manager(t)
	b := new(bytes.Buffer)
	s := &Session{id: "s1", protocol: "ssh", mode: "stream", config: DefaultConfig("ssh"), last: time.Now(), lastUse: time.Now(), connected: true, notify: make(chan struct{}, 1), done: make(chan struct{}), input: b}
	m.sessions[s.id] = s
	return m, s, b
}
func waitActive(t *testing.T, s *Session) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		active := s.active
		s.mu.Unlock()
		if active > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("operation never became active")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestDefaultsConfigurationAtomicAndOverrides(t *testing.T) {
	m, s, wire := localSession(t)
	info, err := m.GetConfig(s.id)
	if err != nil || info.Config.ReadIdleTimeoutMS != 500 || info.Config.ReadTotalTimeoutMS != 30000 || info.Config.ExecTotalTimeoutMS != 30000 || info.Fields["read_idle_timeout_ms"].Description == "" {
		t.Fatal(info, err)
	}
	if _, err = m.Configure(s.id, cfg(map[string]any{"read_idle_timeout_ms": 0, "session_idle_timeout_ms": "30*60*1000", "line_end": `\0`})); err != nil {
		t.Fatal(err)
	}
	v := run(t, m, "sendline_escaped", Request{Session: s.id, Data: `a\xFF`, CollectOutput: noCollect(), Config: cfg(map[string]any{"line_end": `\r\n`})})
	if v.InputState != "sent" || v.WrittenBytes != 4 || !bytes.Equal(wire.Bytes(), []byte{'a', 255, 13, 10}) {
		t.Fatal(v, wire.Bytes())
	}
	info, _ = m.GetConfig(s.id)
	if info.Config.LineEnd != `\0` {
		t.Fatal("override changed global config")
	}
	before := info.Config
	for _, p := range []ConfigPatch{cfg(map[string]any{"read_idle_timeout_ms": 1, "encoding": "unknown"}), cfg(map[string]any{"typo": 1}), cfg(map[string]any{"read_total_timeout_ms": 0}), cfg(map[string]any{"exec_total_timeout_ms": 0}), cfg(map[string]any{"exec_total_timeout_ms": 110001}), cfg(map[string]any{"max_output_bytes": 65537}), cfg(map[string]any{"end_pattern": "["}), cfg(map[string]any{"line_end": `\q`}), cfg(map[string]any{"read_idle_timeout_ms": nil})} {
		if _, err = m.Configure(s.id, p); err == nil {
			t.Fatal("invalid config accepted", p)
		}
	}
	info, _ = m.GetConfig(s.id)
	if info.Config != before {
		t.Fatal("failed patch was not atomic")
	}
	if _, err = m.Operate(context.Background(), "read_available", Request{Session: s.id, Config: cfg(map[string]any{"tcp_keepalive_ms": 0})}); err == nil {
		t.Fatal("connection override accepted")
	}
}
func TestIntegerExpressions(t *testing.T) {
	for source, want := range map[string]int64{`30000`: 30000, `"30*60*1000"`: 1800000, `"(8*1024)/2"`: 4096} {
		got, err := integer(json.RawMessage(source))
		if err != nil || got != want {
			t.Fatal(source, got, err)
		}
	}
	for _, source := range []string{`1.5`, `"1/0"`, `"1/2"`, `"f()"`, `"1<<5"`, `"99999999999999999999999999999999"`, `null`, `true`} {
		if _, err := integer(json.RawMessage(source)); err == nil {
			t.Fatal("unsafe expression accepted", source)
		}
	}
}
func TestSSHExecExitStreamsAndMissingStatus(t *testing.T) {
	addr, key := testutil.SSH(t)
	m := manager(t)
	o := options(addr, "ssh", "exec")
	o.HostKey = key
	id := connect(t, m, o)
	for _, tc := range []struct {
		cmd  string
		code int
	}{{"both", 0}, {"exit7", 7}} {
		v := run(t, m, "exec", Request{Session: id, Command: tc.cmd})
		if v.Reason != "exec_finish" || v.ExitCode == nil || *v.ExitCode != tc.code || v.InputState != "sent" {
			t.Fatal(v)
		}
		if tc.cmd == "both" && (!strings.Contains(v.Output, "stdout") || !strings.Contains(v.Output, "stderr")) {
			t.Fatal(v)
		}
	}
	v := run(t, m, "exec", Request{Session: id, Command: "missing-status"})
	if v.Reason != "exec_finish" || v.ExitCode != nil || v.Warning == "" || !v.Connected {
		t.Fatal(v)
	}
	v = run(t, m, "exec", Request{Session: id, Command: "slow", Config: cfg(map[string]any{"exec_total_timeout_ms": 30})})
	if v.Reason != "total_timeout" || !v.Running || !strings.Contains(v.Output, "first") {
		t.Fatal(v)
	}
	busy := run(t, m, "exec", Request{Session: id, Command: "both"})
	if busy.InputState != "not_sent" || !strings.Contains(busy.Error, "busy") {
		t.Fatal(busy)
	}
	v = run(t, m, "read_until", Request{Session: id})
	if v.Reason != "exec_finish" || v.Running || !strings.Contains(v.Output, "second") {
		t.Fatal(v)
	}
	v = run(t, m, "read_history", Request{Session: id})
	if !strings.Contains(v.Output, "first") || !strings.Contains(v.Output, "second") {
		t.Fatal(v)
	}
	if _, err := m.Operate(context.Background(), "send_text", Request{Session: id, Data: "x"}); err == nil {
		t.Fatal("send accepted in exec mode")
	}
}
func TestSSHHostVerificationAndModes(t *testing.T) {
	addr, _ := testutil.SSH(t)
	m := manager(t)
	o := options(addr, "ssh", "exec")
	o.HostKey = "SHA256:bad"
	if _, err := m.Connect(context.Background(), o); err == nil {
		t.Fatal("bad host key accepted")
	}
	o = options(addr, "telnet", "exec")
	if _, err := m.Connect(context.Background(), o); err == nil {
		t.Fatal("telnet exec accepted")
	}
}
func TestSSHAndTelnetDeviceSimulators(t *testing.T) {
	for _, protocol := range []string{"ssh", "telnet"} {
		t.Run(protocol, func(t *testing.T) {
			m := manager(t)
			var addr, key string
			if protocol == "ssh" {
				addr, key = testutil.SSH(t)
			} else {
				addr = testutil.TelnetSwitch(t)
			}
			o := options(addr, protocol, "stream")
			o.HostKey = key
			o.Config = cfg(map[string]any{"line_end": `\r\n`, "read_idle_timeout_ms": 500, "read_total_timeout_ms": 2000})
			id := connect(t, m, o)
			v := run(t, m, "read_until", Request{Session: id, Config: cfg(map[string]any{"end_pattern": `switch> `})})
			if v.Reason != "match" {
				t.Fatal(v)
			}
			for _, step := range []struct{ line, pattern string }{{"enable", `switch# `}, {"configure terminal", `switch\(config\)# `}, {"interface ethernet 1", `switch\(config-if\)# `}, {"end", `switch# `}, {"show running-config", `--More--`}} {
				v = run(t, m, "sendline_text", Request{Session: id, Data: step.line, Config: cfg(map[string]any{"end_pattern": step.pattern})})
				if v.Reason != "match" {
					t.Fatal(step, v)
				}
			}
			v = run(t, m, "send_text", Request{Session: id, Data: " ", Config: cfg(map[string]any{"end_pattern": `switch# `})})
			if v.Reason != "match" || !strings.Contains(v.Output, "description") {
				t.Fatal(v)
			}
			v = run(t, m, "sendline_text", Request{Session: id, Data: "slow", Config: cfg(map[string]any{"end_pattern": `switch# `})})
			if v.Reason != "match" || !strings.Contains(v.Output, "slow serial") {
				t.Fatal(v)
			}
			v = run(t, m, "sendline_text", Request{Session: id, Data: "quit"})
			if v.Reason != "closed" || v.Connected {
				t.Fatal(v)
			}
		})
	}
}
func TestSendVariantsEncodingAndByteEscapes(t *testing.T) {
	m, s, wire := localSession(t)
	for _, tc := range []struct {
		kind, data, encoding string
		want                 []byte
	}{
		{"send_text", `\r`, "utf-8", []byte(`\r`)},
		{"send_escaped", `\r\n\0\xFF\t`, "utf-8", []byte{13, 10, 0, 255, 9}},
		{"sendline_text", "", "utf-8", []byte{13}},
		{"sendline_escaped", `x\r`, "utf-8", []byte{'x', 13, 13}},
		{"send_text", "中文", "gbk", []byte{0xd6, 0xd0, 0xce, 0xc4}},
		{"send_escaped", `\u4e2d\xFF`, "gbk", []byte{0xd6, 0xd0, 255}},
		{"send_text", "日本", "shift-jis", []byte{0x93, 0xfa, 0x96, 0x7b}},
	} {
		wire.Reset()
		v := run(t, m, tc.kind, Request{Session: s.id, Data: tc.data, CollectOutput: noCollect(), Config: cfg(map[string]any{"encoding": tc.encoding})})
		if v.InputState != "sent" || !bytes.Equal(wire.Bytes(), tc.want) {
			t.Fatal(tc, v, wire.Bytes())
		}
	}
	before := wire.Len()
	for _, tc := range []struct{ data, enc string }{{`\q`, "utf-8"}, {`\x0`, "utf-8"}, {`\uD800`, "utf-8"}, {"😀", "gbk"}} {
		if _, err := m.Operate(context.Background(), "send_escaped", Request{Session: s.id, Data: tc.data, CollectOutput: noCollect(), Config: cfg(map[string]any{"encoding": tc.enc})}); err == nil || wire.Len() != before {
			t.Fatal("invalid input written", tc, err)
		}
	}
}
func TestReadMatchBoundaryBudgetAndHistory(t *testing.T) {
	m, s, _ := localSession(t)
	_, _ = s.Write([]byte("before>after\none\ntwo\n"))
	v := run(t, m, "read_until", Request{Session: s.id, Config: cfg(map[string]any{"end_pattern": ">"})})
	if v.Reason != "match" || v.Output != "before>" || !v.More {
		t.Fatal(v)
	}
	v = run(t, m, "read_available", Request{Session: s.id, Config: cfg(map[string]any{"max_output_bytes": 3})})
	if v.Reason != "output_limit" || v.Output != "aft" {
		t.Fatal(v)
	}
	v = run(t, m, "read_tail", Request{Session: s.id, Lines: 2})
	if v.Output != "one\ntwo\n" {
		t.Fatal(v)
	}
	v = run(t, m, "read_available", Request{Session: s.id})
	if v.Output != "er\none\ntwo\n" {
		t.Fatal("history moved incremental cursor", v)
	}
	var got strings.Builder
	var off int64
	for {
		v = run(t, m, "read_history", Request{Session: s.id, Offset: &off, Config: cfg(map[string]any{"max_output_bytes": 4})})
		got.WriteString(v.Output)
		off = v.End
		if !v.More {
			break
		}
	}
	if got.String() != "before>after\none\ntwo\n" {
		t.Fatal(got.String())
	}
	_, _ = s.Write(bytes.Repeat([]byte{'x'}, HistoryLimit+17))
	v = run(t, m, "read_available", Request{Session: s.id})
	if v.DroppedBytes == 0 || !v.More {
		t.Fatal("history loss not reported", v)
	}
	v = run(t, m, "read_history", Request{Session: s.id})
	if v.DroppedBytes == 0 {
		t.Fatal("historical prefix loss not reported", v)
	}
}
func TestIncrementalDecodeAndErrorPolicies(t *testing.T) {
	for _, tc := range []struct {
		encoding string
		wire     []byte
		want     string
	}{{"utf-8", []byte("中"), "中"}, {"gbk", []byte{0xd6, 0xd0}, "中"}, {"shift-jis", []byte{0x93, 0xfa}, "日"}} {
		m, s, _ := localSession(t)
		_, _ = s.Write(tc.wire[:1])
		r := Request{Session: s.id, Config: cfg(map[string]any{"encoding": tc.encoding})}
		v := run(t, m, "read_available", r)
		if v.Output != "" || !v.More || s.cursor != 0 {
			t.Fatal(tc, v)
		}
		_, _ = s.Write(tc.wire[1:])
		v = run(t, m, "read_available", r)
		if v.Output != tc.want || v.More {
			t.Fatal(tc, v)
		}
	}
	m, s, _ := localSession(t)
	_, _ = s.Write([]byte{255, 'a'})
	v := run(t, m, "read_available", Request{Session: s.id, Config: cfg(map[string]any{"output_decode_error_action": "error"})})
	if v.Reason != "aborted" || v.Error == "" || s.cursor != 0 || !v.Connected {
		t.Fatal(v)
	}
	v = run(t, m, "read_available", Request{Session: s.id, Config: cfg(map[string]any{"output_decode_error_action": "to_hex"})})
	if v.Output != "<FF>a" {
		t.Fatal(v)
	}
	_, _ = s.Write([]byte{255})
	v = run(t, m, "read_available", Request{Session: s.id})
	if v.Output != `\xFF` {
		t.Fatal(v)
	}
	_, _ = s.Write([]byte("中"))
	v = run(t, m, "read_available", Request{Session: s.id, Config: cfg(map[string]any{"max_output_bytes": 1})})
	if v.Reason != "output_limit" || v.MinimumOutputBytes != 3 || v.Output != "" {
		t.Fatal(v)
	}
	v = run(t, m, "read_available", Request{Session: s.id})
	if v.Output != "中" {
		t.Fatal(v)
	}
}
func TestReadTimeoutsCancellationAndSnapshots(t *testing.T) {
	m, s, wire := localSession(t)
	_, err := m.Configure(s.id, cfg(map[string]any{"read_total_timeout_ms": 80, "read_idle_timeout_ms": 0}))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Result, 1)
	go func() { v, _ := m.Operate(context.Background(), "read_until", Request{Session: s.id}); done <- v }()
	waitActive(t, s)
	if _, err := m.Operate(context.Background(), "read_available", Request{Session: s.id}); err == nil {
		t.Fatal("concurrent readers accepted")
	}
	v := run(t, m, "send_text", Request{Session: s.id, Data: "while-reading", CollectOutput: noCollect()})
	if v.InputState != "sent" || wire.String() != "while-reading" {
		t.Fatal(v)
	}
	_, err = m.Configure(s.id, cfg(map[string]any{"read_total_timeout_ms": 1, "read_idle_timeout_ms": 1}))
	if err != nil {
		t.Fatal(err)
	}
	v = <-done
	if v.Reason != "total_timeout" || !v.Connected {
		t.Fatal(v)
	}
	start := time.Now()
	v = run(t, m, "read_until", Request{Session: s.id, Config: cfg(map[string]any{"read_total_timeout_ms": 500, "read_idle_timeout_ms": 40})})
	if v.Reason != "idle_timeout" || time.Since(start) < 35*time.Millisecond || !v.Connected {
		t.Fatal(v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); _, _ = s.Write([]byte("partial")); cancel() }()
	v, err = m.Operate(ctx, "read_until", Request{Session: s.id, Config: cfg(map[string]any{"read_total_timeout_ms": 500, "read_idle_timeout_ms": 0})})
	if err != nil || v.Reason != "cancelled" || !v.Connected || v.Output != "partial" {
		t.Fatal(v, err)
	}
}
func TestBlockedWriteBoundedAndCloseDiscards(t *testing.T) {
	m, s, _ := localSession(t)
	a, b := net.Pipe()
	defer b.Close()
	s.input = a
	s.closer = a
	v := run(t, m, "send_text", Request{Session: s.id, Data: "x", CollectOutput: noCollect(), Config: cfg(map[string]any{"write_timeout_ms": 30})})
	if v.Reason != "aborted" || v.InputState != "unknown" || v.Connected {
		t.Fatal(v)
	}
	_, _ = s.Write([]byte(strings.Repeat("unread", 1000)))
	v, err := m.Close(s.id)
	if err != nil || v.Output != "" || v.Error != "" || v.Reason != "closed" {
		t.Fatal(v, err)
	}
	if _, err = m.Operate(context.Background(), "read_history", Request{Session: s.id}); err == nil {
		t.Fatal("read after explicit close accepted")
	}
	_, _ = s.Write([]byte("late"))
	if len(s.buf) != 0 {
		t.Fatal("late output resurrected discarded buffer")
	}
}
func TestEOFAndAbortedPreserveReceivedOutput(t *testing.T) {
	for _, failure := range []string{"", "network failure"} {
		m, s, _ := localSession(t)
		_, _ = s.Write([]byte("received"))
		s.disconnect(failure)
		v := run(t, m, "read_until", Request{Session: s.id})
		want := "closed"
		if failure != "" {
			want = "aborted"
		}
		if v.Output != "received" || v.Reason != want || v.Connected {
			t.Fatal(v)
		}
	}
}
func TestSessionExpirationAndShortIDs(t *testing.T) {
	addr := testutil.Listen(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	m := manager(t)
	o := options(addr, "telnet", "stream")
	id := connect(t, m, o)
	if id != "s1" {
		t.Fatal(id)
	}
	s, _ := m.Get(id)
	s.mu.Lock()
	s.last = time.Now().Add(-time.Hour)
	s.lastUse = time.Now()
	s.mu.Unlock()
	if s.expire(time.Now()) {
		t.Fatal("unused stream uses endpoint silence")
	}
	s.mu.Lock()
	s.lastUse = time.Now().Add(-time.Hour)
	s.active = 1
	s.mu.Unlock()
	if s.expire(time.Now()) {
		t.Fatal("active reader reclaimed")
	}
	s.mu.Lock()
	s.active = 0
	s.config.SessionIdleTimeoutMS = 0
	s.mu.Unlock()
	if s.expire(time.Now()) {
		t.Fatal("disabled idle reclamation")
	}
	s.mu.Lock()
	s.config.SessionIdleTimeoutMS = 10
	s.mu.Unlock()
	if !s.expire(time.Now()) {
		t.Fatal("expired session retained")
	}
	m.List()
	if id = connect(t, m, o); id != "s2" {
		t.Fatal(id)
	}
}
func TestFullTCPBufferWriteDeadline(t *testing.T) {
	accepted := make(chan *net.TCPConn, 1)
	addr := testutil.Listen(t, func(c net.Conn) { accepted <- c.(*net.TCPConn); _, _ = io.Copy(io.Discard, &neverReader{c}) })
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	peer := <-accepted
	if err = peer.SetReadBuffer(1024); err != nil {
		t.Fatal(err)
	}
	if err = c.(*net.TCPConn).SetWriteBuffer(1024); err != nil {
		t.Fatal(err)
	}
	m, s, _ := localSession(t)
	s.input = c
	s.closer = c
	s.tcp = c.(*net.TCPConn)
	v := run(t, m, "send_text", Request{Session: s.id, Data: strings.Repeat("x", MaxInputLimit), CollectOutput: noCollect(), Config: cfg(map[string]any{"max_input_bytes": MaxInputLimit, "write_timeout_ms": 100})})
	if v.InputState != "unknown" || v.Connected || v.Reason != "aborted" {
		t.Fatal(v)
	}
}

// Wait for socket closure without reading bytes, so the peer receive queue fills.
type neverReader struct{ net.Conn }

func (n *neverReader) Read(p []byte) (int, error) {
	b := make([]byte, 1)
	for {
		_, err := n.Conn.Write(b)
		if err != nil {
			return 0, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestKeepaliveAndQueueCapability(t *testing.T) {
	addr := testutil.Listen(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	m := manager(t)
	id := connect(t, m, options(addr, "telnet", "stream"))
	for _, ms := range []int{0, 1000} {
		if _, err := m.Configure(id, cfg(map[string]any{"tcp_keepalive_ms": ms})); err != nil {
			t.Fatal(err)
		}
	}
	info, err := m.Configure(id, cfg(map[string]any{"backpressure_bytes": 1024}))
	if queueSupported() {
		if err != nil || info.Config.BackpressureBytes != 1024 {
			t.Fatal(info, err)
		}
		s, _ := m.Get(id)
		if _, err := queuedBytes(s.tcp); err != nil {
			t.Fatal(err)
		}
	} else if err == nil {
		t.Fatal("unsupported queue threshold accepted")
	}
}
func TestLongSilentReadBound(t *testing.T) {
	if os.Getenv("SESSMCP_LONG_TEST") != "1" {
		t.Skip("real 110s read deadline")
	}
	addr := testutil.Listen(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	m := manager(t)
	id := connect(t, m, options(addr, "telnet", "stream"))
	start := time.Now()
	v := run(t, m, "read_until", Request{Session: id, Config: cfg(map[string]any{"read_total_timeout_ms": 110000, "read_idle_timeout_ms": 0})})
	if v.Reason != "total_timeout" || !v.Connected || time.Since(start) >= 120*time.Second {
		t.Fatal(v, time.Since(start))
	}
}

func TestDefaultReadIdleIs500ms(t *testing.T) {
	m, s, _ := localSession(t)
	start := time.Now()
	v := run(t, m, "read_until", Request{Session: s.id})
	elapsed := time.Since(start)
	if v.Reason != "idle_timeout" || elapsed < 480*time.Millisecond || elapsed > 2*time.Second || !v.Connected {
		t.Fatal(v, elapsed)
	}
}
func TestWriteDuringReadAndExplicitCloseWakesReader(t *testing.T) {
	m, s, _ := localSession(t)
	done := make(chan Result, 1)
	go func() {
		v, _ := m.Operate(context.Background(), "read_until", Request{Session: s.id, Config: cfg(map[string]any{"read_idle_timeout_ms": 0})})
		done <- v
	}()
	waitActive(t, s)
	_, _ = s.Write([]byte("discard me"))
	if _, err := m.Close(s.id); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-done:
		if v.Reason != "closed" || v.Output != "" {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not interrupt read")
	}
}
func TestRealTCPQueueThresholdRejectsWithoutWriting(t *testing.T) {
	if !queueSupported() {
		t.Skip("OS queue query unavailable")
	}
	ready := make(chan struct{})
	addr := testutil.Listen(t, func(c net.Conn) {
		_ = c.(*net.TCPConn).SetReadBuffer(1024)
		close(ready)
		_, _ = io.Copy(io.Discard, &neverReader{c})
	})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-ready
	tcp := c.(*net.TCPConn)
	if err = tcp.SetWriteBuffer(1 << 20); err != nil {
		t.Fatal(err)
	}
	m, s, _ := localSession(t)
	s.input = c
	s.closer = c
	s.tcp = tcp
	v := run(t, m, "send_text", Request{Session: s.id, Data: strings.Repeat("x", 128<<10), CollectOutput: noCollect(), Config: cfg(map[string]any{"max_input_bytes": 128 << 10, "write_timeout_ms": 1000})})
	if v.InputState != "sent" {
		t.Fatal(v)
	}
	queued, err := queuedBytes(tcp)
	if err != nil || queued == 0 {
		t.Fatal("queue did not retain data", queued, err)
	}
	v = run(t, m, "send_text", Request{Session: s.id, Data: "must-not-send", CollectOutput: noCollect(), Config: cfg(map[string]any{"backpressure_bytes": 1})})
	if v.InputState != "not_sent" || v.QueuedBytes == nil || *v.QueuedBytes < 1 || v.WrittenBytes != 0 || !v.Connected {
		t.Fatal(v)
	}
}

func TestConnectTimeoutAndCloseAllCancelOpening(t *testing.T) {
	addr := testutil.Listen(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	m := manager(t)
	o := options(addr, "ssh", "exec")
	o.Insecure = true
	o.Config = cfg(map[string]any{"connect_timeout_ms": 30})
	start := time.Now()
	if _, err := m.Connect(context.Background(), o); err == nil || time.Since(start) > time.Second {
		t.Fatal("handshake deadline failed", err, time.Since(start))
	}
	o.Config = nil
	done := make(chan error, 1)
	go func() { _, err := m.Connect(context.Background(), o); done <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		opening := m.opening
		m.mu.Unlock()
		if opening > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connect did not start")
		}
		time.Sleep(time.Millisecond)
	}
	m.CloseAll()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("close_all did not cancel connection")
		}
	case <-time.After(time.Second):
		t.Fatal("opening SSH connection not interrupted")
	}
	if len(m.List()) != 0 {
		t.Fatal("late connection resurrected after close_all")
	}
}

func TestSSHExecWaitIgnoresReadSettings(t *testing.T) {
	for _, tc := range []struct {
		name, command    string
		global, override map[string]any
	}{
		{name: "default idle does not stop quiet exec", command: "quiet"},
		{name: "global read deadlines and pattern ignored", command: "slow", global: map[string]any{"read_total_timeout_ms": 1, "read_idle_timeout_ms": 1, "end_pattern": "first"}},
		{name: "per-call read deadlines and pattern ignored", command: "slow", override: map[string]any{"read_total_timeout_ms": 1, "read_idle_timeout_ms": 1, "end_pattern": "first"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, key := testutil.SSH(t)
			m := manager(t)
			o := options(addr, "ssh", "exec")
			o.HostKey = key
			o.Config = cfg(tc.global)
			id := connect(t, m, o)
			before, err := m.GetConfig(id)
			if err != nil {
				t.Fatal(err)
			}
			v := run(t, m, "exec", Request{Session: id, Command: tc.command, Config: cfg(tc.override)})
			if v.Reason != "exec_finish" || v.Running || v.ExitCode == nil || *v.ExitCode != 0 || !v.Connected {
				t.Fatal(v)
			}
			if tc.command == "quiet" && v.Output != "quiet done\n" {
				t.Fatal(v)
			}
			if tc.command == "slow" && (!strings.Contains(v.Output, "first") || !strings.Contains(v.Output, "second")) {
				t.Fatal(v)
			}
			after, _ := m.GetConfig(id)
			if after.Config != before.Config {
				t.Fatal("exec altered persistent config")
			}
		})
	}
}

func TestSSHExecIndependentDeadlineThenExplicitRead(t *testing.T) {
	addr, key := testutil.SSH(t)
	m := manager(t)
	o := options(addr, "ssh", "exec")
	o.HostKey = key
	o.Config = cfg(map[string]any{"exec_total_timeout_ms": "5*1000", "read_total_timeout_ms": 1, "read_idle_timeout_ms": 1, "end_pattern": "first"})
	id := connect(t, m, o)
	v := run(t, m, "exec", Request{Session: id, Command: "slow", Config: cfg(map[string]any{"exec_total_timeout_ms": 30})})
	if v.Reason != "total_timeout" || !v.Running || !v.Connected || v.InputState != "sent" || !strings.Contains(v.Output, "first") {
		t.Fatal(v)
	}
	info, _ := m.GetConfig(id)
	if info.Config.ExecTotalTimeoutMS != 5000 || info.Config.ReadTotalTimeoutMS != 1 || info.Config.ReadIdleTimeoutMS != 1 || info.Config.EndPattern != "first" {
		t.Fatal("override changed config", info)
	}
	// An explicit read is still a read: its own timeout must remain effective.
	v = run(t, m, "read_until", Request{Session: id})
	if v.Reason != "total_timeout" || !v.Running || !v.Connected {
		t.Fatal("explicit read ignored read settings", v)
	}
	v = run(t, m, "read_until", Request{Session: id, Config: cfg(map[string]any{"read_total_timeout_ms": 1000, "read_idle_timeout_ms": 0, "end_pattern": ""})})
	if v.Reason != "exec_finish" || v.Running || v.ExitCode == nil || !strings.Contains(v.Output, "second") {
		t.Fatal(v)
	}
}

func TestSSHExecStillHonorsCancellationAndOutputBudget(t *testing.T) {
	addr, key := testutil.SSH(t)
	m := manager(t)
	o := options(addr, "ssh", "exec")
	o.HostKey = key
	id := connect(t, m, o)
	v := run(t, m, "exec", Request{Session: id, Command: "slow", Config: cfg(map[string]any{"max_output_bytes": 5})})
	if v.Reason != "output_limit" || !v.Running || !v.Connected || v.Output != "first" {
		t.Fatal(v)
	}
	v = run(t, m, "read_until", Request{Session: id})
	if v.Reason != "exec_finish" {
		t.Fatal(v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s, _ := m.Get(id)
	done := make(chan Result, 1)
	go func() { v, _ := m.Operate(ctx, "exec", Request{Session: id, Command: "hang"}); done <- v }()
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("exec did not start")
		}
		time.Sleep(time.Millisecond)
	}
	// Wait for channel startup, so this tests cancellation while collecting.
	for {
		if s.writeMu.TryLock() {
			s.writeMu.Unlock()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("exec channel did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case v = <-done:
		if v.Reason != "cancelled" || !v.Running || !v.Connected || v.InputState != "sent" {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("exec wait ignored cancellation")
	}
}

func TestExecImmediatelyRejectsStreamWithoutSending(t *testing.T) {
	for _, protocol := range []string{"ssh", "telnet"} {
		t.Run(protocol, func(t *testing.T) {
			m, s, wire := localSession(t)
			s.protocol = protocol
			s.readMu.Lock()
			defer s.readMu.Unlock()
			s.writeMu.Lock()
			defer s.writeMu.Unlock()
			before := s.lastUse
			_, err := m.Operate(context.Background(), "exec", Request{Session: s.id, Command: "must-not-execute"})
			if err == nil || !strings.Contains(err.Error(), "non-interactive SSH mode=exec") || !strings.Contains(err.Error(), "nothing sent") {
				t.Fatal("expected direct mode rejection", err)
			}
			if wire.Len() != 0 || s.active != 0 || s.running || s.lastUse != before {
				t.Fatal("rejected exec changed stream state")
			}
		})
	}
}
