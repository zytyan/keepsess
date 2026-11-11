package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zytyan/keepsess/internal/buildinfo"
	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/history"
	"github.com/zytyan/keepsess/internal/settings"
	"github.com/zytyan/keepsess/internal/web"
)

type host struct {
	runtime Runtime
	ctx     context.Context
	cancel  context.CancelFunc
	cfg     settings.Config
	token   string
	root    *history.Store
	web     *web.Server
	mu      sync.Mutex
	peers   map[string]*peer
	closing bool
	wg      sync.WaitGroup
}

func Run(ctx context.Context, c settings.Config) error {
	if !c.Web.Enabled {
		return errors.New("web.enabled=false; daemon is disabled")
	}
	if err := os.MkdirAll(c.RuntimeDir(), 0700); err != nil {
		return err
	}
	configHash := c.Fingerprint()
	var handoff *os.File
	if os.Getenv("KEEPSESS_UPGRADE_TOKEN") != "" {
		var e error
		handoff, e = namedLock(c, "handoff.lock")
		if e != nil {
			return e
		}
		defer handoff.Close()
	}
	ticket, candidate := candidateTicket(c)
	if handoff != nil && !candidate {
		return errors.New("upgrade ticket expired or candidate digest mismatch")
	}
	var lifecycle *os.File
	if !candidate {
		var e error
		lifecycle, e = gate(c)
		if e != nil {
			return nil
		}
		defer lifecycle.Close()
		if e = recoverUpgrade(ctx, c); e != nil {
			return e
		}
	} else {
		if ticket.Listen != "" {
			c.Web.Listen = ticket.Listen
			c.Web.BasePath = ticket.BasePath
		}
	}
	lock, err := os.OpenFile(filepath.Join(c.RuntimeDir(), "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = acquireLock(lock); err != nil {
		if !candidate {
			return nil
		}
		waitCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		for err != nil {
			if e := pause(waitCtx); e != nil {
				return e
			}
			err = acquireLock(lock)
		}
	} // Another starter won.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		return err
	}
	root, err := history.View(c.History.Dir, ".daemon")
	if err != nil {
		return err
	}
	defer root.Close()
	h := &host{ctx: ctx, cancel: cancel, cfg: c, token: hex.EncodeToString(tokenBytes), root: root, peers: make(map[string]*peer)}
	m := core.NewManager()
	defer m.CloseAll()
	h.web, err = web.Start(ctx, m, root, web.Options{StrictListen: candidate && ticket.Listen != "", SettingsHome: c.Home, Listen: c.Web.Listen, BasePath: c.Web.BasePath, PublicURL: c.Web.PublicURL, Registry: h, Internal: http.HandlerFunc(h.internal)})
	if err != nil {
		return err
	}
	defer func() {
		h.mu.Lock()
		h.closing = true
		for _, p := range h.peers {
			p.close()
		}
		h.mu.Unlock()
		h.web.Close()
		h.wg.Wait()
	}()
	listenHost, port, _ := net.SplitHostPort(h.web.Info().Listen)
	if ip := net.ParseIP(listenHost); ip == nil || ip.IsUnspecified() {
		if strings.Contains(listenHost, ":") {
			listenHost = "::1"
		} else {
			listenHost = "127.0.0.1"
		}
	}
	runtime := Runtime{PID: os.Getpid(), Protocol: protocol, Token: h.token, Endpoint: "http://" + net.JoinHostPort(listenHost, port) + h.web.Info().BasePath + "internal/", HistoryDir: c.History.Dir, HistoryMaxBytes: c.History.MaxBytes, ConfigHash: configHash, Info: h.web.Info()}
	runtime.StartID = h.web.Info().InstanceID
	runtime.Version = buildinfo.String()
	runtime.BinarySHA256, err = ownDigest()
	if err != nil {
		return err
	}
	runtime.Capabilities = capabilities
	h.runtime = runtime
	b, _ := json.Marshal(runtime)
	tmp := runtimePath(c) + ".tmp"
	_ = os.Remove(tmp)
	f, e := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, runtimePath(c)); err != nil {
		return err
	}
	if lifecycle != nil {
		lifecycle.Close()
	}
	if handoff != nil {
		handoff.Close()
	}
	defer os.Remove(runtimePath(c))
	<-ctx.Done()
	return nil
}
func (h *host) internal(w http.ResponseWriter, r *http.Request) {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(remote)
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err != nil || ip == nil || !isLocalIP(ip) || subtle.ConstantTimeCompare([]byte(token), []byte(h.token)) != 1 {
		http.Error(w, "local daemon authorization required", 403)
		return
	}
	switch filepath.Base(r.URL.Path) {
	case "status":
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		v := h.runtime
		v.Token = ""
		v.Endpoint = ""
		_ = json.NewEncoder(w).Encode(v)
	case "info":
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		_ = json.NewEncoder(w).Encode(h.web.Info())
	case "stop":
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		w.WriteHeader(200)
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		h.cancel()
	case "agent":
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		h.agent(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (h *host) Instances() ([]history.Instance, error) {
	instances, err := h.root.Instances()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range instances {
		p := h.peers[instances[i].ID]
		instances[i].Current = p != nil && p.ctx.Err() == nil
		if p != nil {
			instances[i].PID = p.pid
		}
	}
	sort.SliceStable(instances, func(i, j int) bool {
		if instances[i].Current != instances[j].Current {
			return instances[i].Current
		}
		return instances[i].Started.After(instances[j].Started)
	})
	return instances, nil
}
func (h *host) Lookup(instance string) (web.Backend, error) {
	if instance == "" {
		return web.Backend{Store: h.root, Context: h.ctx}, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if p := h.peers[instance]; p != nil && p.ctx.Err() == nil {
		return web.Backend{Manager: p, Store: p.store, Context: p.ctx}, nil
	}
	return web.Backend{Store: h.root, Context: h.ctx}, nil
}
func (h *host) agent(w http.ResponseWriter, r *http.Request) {
	// Browser requests are never accepted on the private process channel.
	if r.Header.Get("Origin") != "" {
		http.Error(w, "not a browser endpoint", 403)
		return
	}
	up := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(*http.Request) bool { return true }}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	c.SetReadLimit(2 << 20)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello message
	if err = readMessage(c, &hello); err != nil {
		return
	}
	if hello.Type != "register" || hello.Protocol != protocol || hello.Instance == "" || hello.PID < 1 {
		return
	}
	// Only an existing journal in our configured root can become live.
	view, err := history.View(h.cfg.History.Dir, hello.Instance)
	if err != nil {
		return
	}
	if _, err = view.Read(hello.Instance, 0, 1); err != nil {
		view.Close()
		return
	}
	ctx, cancel := context.WithCancel(h.ctx)
	p := &peer{id: hello.Instance, pid: hello.PID, c: c, store: view, ctx: ctx, cancel: cancel, pending: make(map[uint64]chan message), writer: make(chan struct{}, 1), capabilities: hello.Capabilities}
	defer p.close()
	view.Update(hello.History)
	h.mu.Lock()
	if h.closing || len(h.peers) >= 64 || h.peers[p.id] != nil {
		h.mu.Unlock()
		return
	}
	h.peers[p.id] = p
	h.wg.Add(1)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.peers[p.id] == p {
			delete(h.peers, p.id)
		}
		h.mu.Unlock()
		h.wg.Done()
	}()
	if err = p.write(p.ctx, message{Type: "registered", Protocol: protocol, Capabilities: capabilities}); err != nil {
		return
	}
	for {
		_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
		kind, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		if kind != websocket.BinaryMessage {
			return
		}
		var msg message
		if decodeMessage(data, &msg) != nil {
			return
		}
		switch msg.Type {
		case "event":
			if msg.Event == nil || view.Observe(*msg.Event) != nil {
				return
			}
		case "heartbeat":
			view.Update(msg.History)
			if p.write(p.ctx, message{Type: "heartbeat"}) != nil {
				return
			}
		case "result":
			p.mu.Lock()
			response := p.pending[msg.ID]
			p.mu.Unlock()
			if response != nil {
				select {
				case response <- msg:
				default:
				}
			}
		default:
			continue
		}
	}
}

type message struct {
	Capabilities []string      `json:"capabilities,omitempty"`
	Type         string        `json:"type"`
	Protocol     int           `json:"protocol,omitempty"`
	Instance     string        `json:"instance,omitempty"`
	PID          int           `json:"pid,omitempty"`
	ID           uint64        `json:"id,omitempty"`
	Kind         string        `json:"kind,omitempty"`
	Request      core.Request  `json:"request,omitempty"`
	Event        *core.Event   `json:"event,omitempty"`
	History      history.State `json:"history,omitempty"`
	Result       core.Result   `json:"result,omitempty"`
	Tasks        []core.Result `json:"tasks,omitempty"`
	Error        string        `json:"error,omitempty"`
}
type peer struct {
	capabilities []string
	id           string
	pid          int
	c            *websocket.Conn
	store        *history.Store
	ctx          context.Context
	cancel       context.CancelFunc
	once         sync.Once
	writer       chan struct{}
	mu           sync.Mutex
	next         uint64
	pending      map[uint64]chan message
}

func (p *peer) close()             { p.once.Do(func() { p.cancel(); _ = p.c.Close(); p.store.Close() }) }
func (p *peer) InstanceID() string { return p.id }

var errNotSubmitted = errors.New("daemon channel busy or cancelled; nothing submitted")

func (p *peer) write(ctx context.Context, m message) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case p.writer <- struct{}{}:
	case <-ctx.Done():
		return errNotSubmitted
	case <-p.ctx.Done():
		return errNotSubmitted
	case <-timer.C:
		return errNotSubmitted
	}
	defer func() { <-p.writer }()
	if ctx.Err() != nil || p.ctx.Err() != nil {
		return errNotSubmitted
	}
	deadline := time.Now().Add(3 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = p.c.SetWriteDeadline(deadline)
	err := writeMessage(p.c, m)
	if err != nil {
		p.close()
	}
	return err
}
func (p *peer) call(ctx context.Context, kind string, r core.Request) (message, error) {
	if !supports(p.capabilities, kind) {
		return message{Result: core.Result{InputState: "not_sent"}}, errors.New("unsupported Agent capability; nothing submitted")
	}
	p.mu.Lock()
	if len(p.pending) >= 64 {
		p.mu.Unlock()
		return message{Result: core.Result{InputState: "not_sent"}}, errors.New("daemon operation limit; nothing submitted")
	}
	p.next++
	id := p.next
	response := make(chan message, 1)
	p.pending[id] = response
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	if err := p.write(ctx, message{Type: "operation", ID: id, Kind: kind, Request: r}); err != nil {
		if errors.Is(err, errNotSubmitted) {
			return message{Result: core.Result{InputState: "not_sent"}}, err
		}
		return message{}, fmt.Errorf("agent channel failed; operation outcome unknown; do not replay: %w", err)
	}
	select {
	case reply := <-response:
		if reply.Error != "" {
			return reply, errors.New(reply.Error)
		}
		return reply, nil
	case <-ctx.Done():
		return message{}, errors.New("daemon wait ended; submitted operation outcome unknown; do not replay")
	case <-p.ctx.Done():
		return message{}, errors.New("agent disconnected; submitted operation outcome unknown; do not replay")
	}
}
func (p *peer) TaskList(session string) ([]core.Result, error) {
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	m, err := p.call(ctx, "task_list", core.Request{Session: session})
	return m.Tasks, err
}
func (p *peer) Operate(ctx context.Context, kind string, r core.Request) (core.Result, error) {
	m, err := p.call(ctx, kind, r)
	if err != nil && m.Result.InputState == "" {
		m.Result.InputState = "unknown"
	}
	return m.Result, err
}

// URL is kept local even when the displayed Web URL points at a reverse proxy.
func agentURL(r Runtime) (string, error) {
	u, err := url.Parse(r.Endpoint + "agent")
	if err != nil {
		return "", err
	}
	u.Scheme = "ws"
	return u.String(), nil
}

func (p *peer) WebCapabilities() []string { return append([]string(nil), p.capabilities...) }
func (p *peer) WebOperate(ctx context.Context, kind string, r core.Request) (core.Result, error) {
	return p.Operate(ctx, kind, r)
}
