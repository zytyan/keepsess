// Package web serves the embedded human terminal. It shares core operations,
// never MCP readers, and defaults each individual socket to read-only.
package web

import (
	"compress/flate"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/gorilla/websocket"
	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/history"
)

//go:embed assets
var assets embed.FS

type Options struct{ Listen, BasePath, PublicURL string }
type Info struct {
	InstanceID string        `json:"instance_id"`
	Enabled    bool          `json:"enabled"`
	Listen     string        `json:"listen,omitempty"`
	URL        string        `json:"url,omitempty"`
	BasePath   string        `json:"base_path,omitempty"`
	Error      string        `json:"error,omitempty"`
	History    history.State `json:"history"`
}
type Server struct {
	Manager   *core.Manager
	Store     *history.Store
	info      Info
	public    *url.URL
	listener  net.Listener
	http      *http.Server
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	clientsMu sync.Mutex
	clients   map[*websocket.Conn]bool
	replays   chan struct{}
}

func Start(parent context.Context, m *core.Manager, store *history.Store, o Options) (*Server, error) {
	if o.Listen == "" {
		o.Listen = "127.0.0.1:8765"
	}
	if o.BasePath == "" {
		o.BasePath = "/"
	}
	if !strings.HasPrefix(o.BasePath, "/") || strings.HasPrefix(o.BasePath, "//") || strings.IndexFunc(o.BasePath, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || strings.ContainsAny(o.BasePath, "?#\\{}%") || path.Clean(o.BasePath) != strings.TrimSuffix(o.BasePath, "/") && o.BasePath != "/" {
		return nil, errors.New("web-base-path must be a clean absolute URL path")
	}
	o.BasePath = strings.TrimSuffix(o.BasePath, "/") + "/"
	var public *url.URL
	var err error
	if o.PublicURL != "" {
		public, err = url.Parse(o.PublicURL)
		if err != nil || public.Host == "" || (public.Scheme != "http" && public.Scheme != "https") || public.User != nil || public.RawQuery != "" || public.Fragment != "" {
			return nil, errors.New("web-public-url must be an absolute http(s) URL without credentials/query/fragment")
		}
	}
	host, port, err := net.SplitHostPort(o.Listen)
	if err != nil {
		return nil, err
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 0 || p > 65535 {
		return nil, errors.New("invalid web port")
	}
	var ln net.Listener
	for i := 0; i <= 101; i++ {
		candidate := p + i
		if p == 0 || i == 101 || candidate > 65535 {
			candidate = 0
		}
		ln, err = net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(candidate)))
		if err == nil {
			break
		}
		if !addressInUse(err) {
			return nil, err
		}
		if candidate == 0 {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	s := &Server{Manager: m, Store: store, public: public, listener: ln, ctx: ctx, cancel: cancel, clients: make(map[*websocket.Conn]bool), replays: make(chan struct{}, 4)}
	accessHost, accessPort, _ := net.SplitHostPort(ln.Addr().String())
	if accessHost == "" || accessHost == "0.0.0.0" || accessHost == "::" {
		accessHost = "127.0.0.1"
	}
	s.info = Info{InstanceID: m.InstanceID(), Enabled: true, Listen: ln.Addr().String(), URL: "http://" + net.JoinHostPort(accessHost, accessPort) + o.BasePath, BasePath: o.BasePath}
	if public != nil {
		s.info.URL = strings.TrimSuffix(public.String(), "/") + "/"
	}
	mux := http.NewServeMux()
	sub, _ := fs.Sub(assets, "assets")
	files := http.FileServer(http.FS(sub))
	mux.HandleFunc(o.BasePath+"api/instances", s.instances)
	mux.HandleFunc(o.BasePath+"api/catalog", s.catalog)
	mux.HandleFunc(o.BasePath+"api/history", s.read)
	mux.HandleFunc(o.BasePath+"api/replay", s.replay)
	mux.HandleFunc(o.BasePath+"api/info", func(w http.ResponseWriter, r *http.Request) { respond(w, s.Info()) })
	mux.HandleFunc(o.BasePath+"ws", s.socket)
	mux.Handle(o.BasePath, http.StripPrefix(o.BasePath, fontCache(files)))
	if o.BasePath != "/" {
		mux.HandleFunc(strings.TrimSuffix(o.BasePath, "/"), func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, o.BasePath, http.StatusTemporaryRedirect)
		})
	}
	s.http = &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, WriteTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); _ = s.http.Serve(ln) }()
	return s, nil
}
func (s *Server) Info() Info { i := s.info; i.History = s.Store.Status(); return i }
func (s *Server) Close() {
	s.cancel()
	_ = s.http.Close()
	s.clientsMu.Lock()
	for c := range s.clients {
		_ = c.Close()
	}
	s.clientsMu.Unlock()
	s.wg.Wait()
}
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		if !s.hostOK(r.Host) {
			http.Error(w, "unrecognized Host; use an IP/localhost address or configure --web-public-url for a DNS name", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !s.originOK(r, origin) {
			http.Error(w, "cross-origin request denied", 403)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'")
		next.ServeHTTP(w, r)
	})
}

// A proxy/tunnel may change the externally visible IP or port. IP literals
// and localhost do not depend on DNS, so permit them without pinning a public
// URL. DNS names still require an explicit public URL to retain the local
// unauthenticated service's protection against DNS rebinding.
func (s *Server) hostOK(authority string) bool {
	host, _, ok := parseAuthority(authority, "http")
	if !ok {
		return false
	}
	if host == "localhost" {
		return true
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if s.public != nil {
		return sameAuthority(authority, s.public.Host, s.public.Scheme)
	}
	return false
}
func parseAuthority(authority, scheme string) (string, string, bool) {
	if (scheme != "http" && scheme != "https") || strings.ContainsAny(authority, "/?#@\\") {
		return "", "", false
	}
	u, err := url.Parse("//" + authority)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return "", "", false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || strings.IndexFunc(host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", "", false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.Unmap().String()
	} else if strings.ContainsAny(host, ":%[]\\") {
		return "", "", false
	}
	port := u.Port()
	if port == "" {
		if strings.HasSuffix(authority, ":") {
			return "", "", false
		}
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", "", false
	}
	return host, strconv.Itoa(n), true
}
func sameAuthority(a, b, scheme string) bool {
	ah, ap, aok := parseAuthority(a, scheme)
	bh, bp, bok := parseAuthority(b, scheme)
	return aok && bok && ah == bh && ap == bp
}
func (s *Server) originOK(r *http.Request, origin string) bool {
	if strings.ContainsAny(origin, "?#") {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Host == "" {
		return false
	}
	if s.public != nil && u.Scheme == s.public.Scheme && sameAuthority(u.Host, s.public.Host, u.Scheme) {
		return true
	}
	// Match the browser's authority, not the backend socket's address/protocol.
	// This also works for HTTPS/WSS terminated at a Host-preserving proxy. Do not
	// infer a different authority from untrusted Forwarded/X-Forwarded-* headers.
	return sameAuthority(u.Host, r.Host, u.Scheme)
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, e error) { http.Error(w, e.Error(), http.StatusBadRequest) }
func (s *Server) instances(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.Instances()
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, v)
}
func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	offset, e := strconv.Atoi(defaultString(r.URL.Query().Get("offset"), "0"))
	if e != nil || offset < 0 {
		fail(w, errors.New("invalid offset"))
		return
	}
	v, more, e := s.Store.Catalog(r.URL.Query().Get("instance"), offset, 100)
	if errors.Is(e, history.ErrIndexing) {
		respond(w, map[string]any{"records": []any{}, "indexing": true, "more": false, "next": offset})
		return
	}
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, map[string]any{"records": v, "more": more, "next": offset + len(v)})
}
func (s *Server) read(w http.ResponseWriter, r *http.Request) {
	after, e := strconv.ParseUint(defaultString(r.URL.Query().Get("after"), "0"), 10, 64)
	if e != nil {
		fail(w, e)
		return
	}
	p, e := s.Store.Read(r.URL.Query().Get("instance"), after, 128)
	if e != nil {
		fail(w, e)
		return
	}
	filterPage(&p, r.URL.Query().Get("session"))
	respond(w, p)
}
func filterPage(p *history.Page, session string) {
	if session == "" {
		return
	}
	out := p.Events[:0]
	for _, e := range p.Events {
		if e.Session == session {
			out = append(out, e)
		}
	}
	p.Events = out
}
func defaultString(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

type request struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Session     string `json:"session"`
	Handle      string `json:"handle"`
	Data        string `json:"data"`
	Command     string `json:"command"`
	Cwd         string `json:"cwd"`
	Interactive bool   `json:"interactive"`
	PTY         bool   `json:"pty"`
	Enabled     bool   `json:"enabled"`
}

func (s *Server) socket(w http.ResponseWriter, r *http.Request) {
	if instance := r.URL.Query().Get("instance"); instance != "" && instance != s.Manager.InstanceID() {
		http.Error(w, "instance changed; refresh page; input was not sent", http.StatusConflict)
		return
	}
	// A browser must present Origin even though ordinary local HTTP is public.
	if !s.originOK(r, r.Header.Get("Origin")) {
		http.Error(w, "valid Origin required", 403)
		return
	}
	after, err := strconv.ParseUint(defaultString(r.URL.Query().Get("after"), "0"), 10, 64)
	if err != nil {
		fail(w, err)
		return
	}
	sub, pending, written, err := s.Store.Subscribe()
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	defer sub.Close()
	binaryMode := r.URL.Query().Get("format") == "binary-v1"
	up := websocket.Upgrader{EnableCompression: binaryMode, ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(r *http.Request) bool { return s.originOK(r, r.Header.Get("Origin")) }}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	if binaryMode {
		_ = c.SetCompressionLevel(flate.BestSpeed)
	}
	s.clientsMu.Lock()
	if s.ctx.Err() != nil {
		s.clientsMu.Unlock()
		_ = c.Close()
		return
	}
	s.clients[c] = true
	s.wg.Add(1)
	s.clientsMu.Unlock()
	defer func() { _ = c.Close(); s.clientsMu.Lock(); delete(s.clients, c); s.clientsMu.Unlock(); s.wg.Done() }()
	session := r.URL.Query().Get("session")
	if session == "" {
		return
	}
	c.SetReadLimit(2 << 20)
	_ = c.SetReadDeadline(time.Now().Add(45 * time.Second))
	c.SetPongHandler(func(string) error { return c.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	replies := make(chan any, 16)
	var unlocked atomic.Bool
	socketCtx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		for {
			var req request
			if err := c.ReadJSON(&req); err != nil {
				return
			}
			if req.Type == "unlock" {
				unlocked.Store(req.Enabled)
				select {
				case replies <- map[string]any{"type": "lock", "unlocked": req.Enabled}:
				case <-socketCtx.Done():
					return
				}
				continue
			}
			if !unlocked.Load() {
				select {
				case replies <- map[string]any{"type": "receipt", "id": req.ID, "error": "Web terminal is read-only; unlock first", "input_state": "not_sent"}:
				case <-socketCtx.Done():
					return
				}
				continue
			}
			// Serialize browser operations without an input backlog. A slow write is
			// bounded by core, and reconnect never resubmits it.
			if req.Session != session {
				select {
				case replies <- map[string]any{"type": "receipt", "id": req.ID, "error": "session mismatch; nothing sent"}:
				case <-socketCtx.Done():
					return
				}
				continue
			}
			result, e := s.operate(req)
			reply := map[string]any{"type": "receipt", "id": req.ID, "result": result}
			if e != nil {
				reply["error"] = e.Error()
			}
			select {
			case replies <- reply:
			case <-socketCtx.Done():
				return
			}
		}
	}()
	write := func(v any) error { _ = c.SetWriteDeadline(time.Now().Add(3 * time.Second)); return c.WriteJSON(v) }
	if write(map[string]any{"type": "hello", "unlocked": false, "info": s.Info()}) != nil {
		return
	}
	batch := outputBatch{emit: func(data []byte) error {
		_ = c.SetWriteDeadline(time.Now().Add(3 * time.Second))
		return c.WriteMessage(websocket.BinaryMessage, data)
	}}
	// A one-shot timer bounds latency without a permanent per-client 50Hz tick.
	var timer *time.Timer
	var flushC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	schedule := func() {
		if flushC == nil {
			if timer == nil {
				timer = time.NewTimer(20 * time.Millisecond)
			} else {
				timer.Reset(20 * time.Millisecond)
			}
			flushC = timer.C
		}
	}
	checkpoint := after
	flush := func() error {
		if err := batch.flush(); err != nil {
			return err
		}
		if binaryMode && after > checkpoint {
			if err := write(map[string]any{"type": "checkpoint", "seq": after}); err != nil {
				return err
			}
			checkpoint = after
		}
		return nil
	}
	send := func(e core.Event) error {
		if e.Seq <= after {
			return nil
		}
		after = e.Seq
		if binaryMode {
			schedule()
		}
		if e.Kind == "output" || e.Kind == "input" {
			if e.Session != session {
				return nil
			}
			if h, ok := r.URL.Query()["handle"]; binaryMode && ok && e.Handle != h[0] {
				return nil
			}
		}
		if binaryMode && e.Kind == "output" {
			return batch.add(e)
		}
		if err := batch.flush(); err != nil {
			return err
		}
		return write(map[string]any{"type": "event", "event": e})
	}
	// Subscribe before replay. Pending records cover the append queue; duplicates
	// visible through both disk and pending/live are removed by sequence number.
	for after < written {
		p, e := s.Store.Read(s.Manager.InstanceID(), after, 4096)
		if e != nil {
			_ = write(map[string]any{"type": "error", "error": e.Error()})
			break
		}
		if len(p.Events) == 0 {
			break
		}
		for _, event := range p.Events {
			if err = send(event); err != nil {
				return
			}
		}
		if p.Next <= after && !p.More {
			break
		}
		if socketCtx.Err() != nil {
			return
		}
	}
	for _, e := range pending {
		if send(e) != nil {
			return
		}
	}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-socketCtx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1013, "slow consumer; reconnect to replay history"), time.Now().Add(time.Second))
				return
			}
			sub.Ack(e)
			if send(e) != nil {
				return
			}
		case <-flushC:
			flushC = nil
			if flush() != nil {
				return
			}
		case reply := <-replies:
			if flush() != nil {
				return
			}
			if write(reply) != nil {
				return
			}
		case <-tick.C:
			if flush() != nil {
				return
			}
			if write(map[string]any{"type": "status", "info": s.Info()}) != nil {
				return
			}
			if c.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)) != nil {
				return
			}
		}
	}
}
func (s *Server) operate(req request) (core.Result, error) {
	if len(req.ID) > 128 {
		return core.Result{}, errors.New("invalid request ID")
	}
	if req.Handle != "" {
		tasks, err := s.Manager.TaskList(req.Session)
		if err != nil {
			return core.Result{}, err
		}
		found := false
		for _, t := range tasks {
			if t.Handle == req.Handle {
				found = true
				break
			}
		}
		if !found {
			return core.Result{}, errors.New("task not retained in this session; nothing sent")
		}
	}
	no := false
	r := core.Request{Session: req.Session, Handle: req.Handle, Data: req.Data, Command: req.Command, Cwd: req.Cwd, Interactive: req.Interactive, PTY: req.PTY, CollectOutput: &no}
	kind := "send_text"
	switch req.Type {
	case "input":
		if req.Command != "" || req.Cwd != "" || req.Interactive || req.PTY {
			return core.Result{}, errors.New("unexpected input options")
		}
		if req.Handle != "" {
			r.Session = ""
		}
	case "exec":
		if req.Handle != "" || req.Data != "" {
			return core.Result{}, errors.New("unexpected exec options")
		}
		kind = "exec"
	default:
		return core.Result{}, errors.New("unsupported Web operation; nothing sent")
	}
	// Socket loss stops observation, not a command or a write already submitted.
	ctx, cancel := context.WithTimeout(s.ctx, core.CallLimit)
	defer cancel()
	return s.Manager.Operate(ctx, kind, r)
}
func addressInUse(err error) bool { return isAddressInUse(err) }

// Only fingerprinted, embedded font bytes are immutable. History and UI code
// remain no-store so an upgrade cannot run stale protocol code.
func fontCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "fonts/JetBrainsMono-662a196d58f1.ttf", "/fonts/JetBrainsMono-662a196d58f1.ttf", "fonts/NotoSansSC-2057fd5e1572.woff2", "/fonts/NotoSansSC-2057fd5e1572.woff2":
			tag := `"` + path.Base(r.URL.Path) + `"`
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Header().Set("ETag", tag)
			if r.Header.Get("If-None-Match") == tag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
