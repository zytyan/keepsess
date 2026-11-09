package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/history"
	"github.com/zytyan/keepsess/internal/settings"
	"github.com/zytyan/keepsess/internal/web"
)

type Client struct {
	cfg        settings.Config
	manager    *core.Manager
	store      *history.Store
	launch     func(settings.Config) error
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	workers    sync.WaitGroup
	slots      chan struct{}
	mu         sync.Mutex
	info       web.Info
	connection *websocket.Conn
}

func Connect(parent context.Context, c settings.Config, m *core.Manager, st *history.Store, r Runtime, launch func(settings.Config) error) *Client {
	ctx, cancel := context.WithCancel(parent)
	client := &Client{cfg: c, manager: m, store: st, launch: launch, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 32), info: r.Info}
	client.wg.Add(1)
	ready := make(chan struct{})
	go client.run(r, ready)
	select {
	case <-ready:
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
	}
	return client
}
func (c *Client) Info() web.Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	info := c.info
	info.InstanceID = c.manager.InstanceID()
	info.History = c.store.Status()
	return info
}
func (c *Client) Close() {
	c.cancel()
	c.mu.Lock()
	if c.connection != nil {
		_ = c.connection.Close()
	}
	c.mu.Unlock()
	c.wg.Wait()
	c.workers.Wait()
}
func (c *Client) run(r Runtime, ready chan struct{}) {
	defer c.wg.Done()
	var once sync.Once
	for c.ctx.Err() == nil {
		err := c.attach(r, func() { once.Do(func() { close(ready) }) })
		c.mu.Lock()
		c.info.Enabled = false
		if err != nil {
			c.info.Error = "daemon link lost; Web input outcomes may be unknown; no replay: " + err.Error()
		}
		c.connection = nil
		c.mu.Unlock()
		once.Do(func() { close(ready) })
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(time.Second):
		}
		// Read configuration anew when restarting a crashed/stopped daemon. A live
		// process keeps its own journal path; it cannot migrate while commands run.
		cfg, e := settings.LoadHome(c.cfg.Home)
		if e != nil || !cfg.Web.Enabled {
			continue
		}
		next, e := Ensure(c.ctx, cfg, c.launch)
		if e != nil {
			continue
		}
		if next.HistoryDir != c.cfg.History.Dir {
			c.mu.Lock()
			c.info.Error = "history.dir changed; this running Agent still uses the old journal; restart it to migrate"
			c.mu.Unlock()
			continue
		}
		r = next
	}
}
func (c *Client) attach(r Runtime, onReady func()) error {
	sub, pending, _, err := c.store.Subscribe()
	if err != nil {
		return err
	}
	defer sub.Close()
	u, err := agentURL(r)
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second, ReadBufferSize: 4096, WriteBufferSize: 4096}
	conn, _, err := dialer.DialContext(c.ctx, u, http.Header{"Authorization": []string{"Bearer " + r.Token}})
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetReadLimit(2 << 20)
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	c.mu.Lock()
	c.connection = conn
	c.mu.Unlock()
	write := func(m message) error {
		_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		return conn.WriteJSON(m)
	}
	if err = write(message{Type: "register", Protocol: protocol, Instance: c.manager.InstanceID(), PID: os.Getpid(), History: c.store.Status()}); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ack message
	if err = conn.ReadJSON(&ack); err != nil {
		return err
	}
	if ack.Type != "registered" || ack.Protocol != protocol {
		return errors.New("daemon registration rejected")
	}
	c.mu.Lock()
	c.info = r.Info
	c.info.Enabled = true
	c.info.Error = ""
	c.mu.Unlock()
	onReady()
	responses := make(chan message, 64)
	readerDone := make(chan struct{})
	defer func() { cancel(); _ = conn.Close(); <-readerDone }()
	go func() {
		defer close(readerDone)
		defer cancel()
		for {
			_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
			var req message
			if conn.ReadJSON(&req) != nil {
				return
			}
			if req.Type == "heartbeat" {
				continue
			}
			if req.Type != "operation" {
				return
			}
			select {
			case c.slots <- struct{}{}:
			default:
				select {
				case responses <- message{Type: "result", ID: req.ID, Error: "Agent operation limit; nothing submitted", Result: core.Result{InputState: "not_sent"}}:
				case <-ctx.Done():
					return
				}
				continue
			}
			c.workers.Add(1)
			go func(req message) {
				defer c.workers.Done()
				defer func() { <-c.slots }()
				reply := message{Type: "result", ID: req.ID}
				callCtx, stop := context.WithTimeout(c.ctx, core.CallLimit)
				defer stop()
				var err error
				switch req.Kind {
				case "task_list":
					reply.Tasks, err = c.manager.TaskList(req.Request.Session)
				case "exec", "send_text":
					reply.Result, err = c.manager.Operate(callCtx, req.Kind, req.Request)
				default:
					err = errors.New("unsupported daemon operation; nothing submitted")
				}
				if err != nil {
					reply.Error = err.Error()
				}
				select {
				case responses <- reply:
				case <-ctx.Done():
				}
			}(req)
		}
	}()
	eventsSinceCheckpoint := 0
	lastCheckpoint := time.Now()
	checkpoint := func() error {
		eventsSinceCheckpoint = 0
		lastCheckpoint = time.Now()
		return write(message{Type: "heartbeat", History: c.store.Status()})
	}
	emit := func(e core.Event) error {
		// Do not retain a second of already-written output in the daemon's pending
		// window. High-rate small writes need checkpoints as well as idle heartbeats.
		eventsSinceCheckpoint++
		if eventsSinceCheckpoint >= 64 || time.Since(lastCheckpoint) >= 20*time.Millisecond {
			if err := checkpoint(); err != nil {
				return err
			}
		}
		if e.Kind == "output" {
			b, err := web.EncodeOutput(e)
			if err != nil {
				return err
			}
			_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			return conn.WriteMessage(websocket.BinaryMessage, b)
		}
		return write(message{Type: "event", Event: &e})
	}
	for _, event := range pending {
		if err = emit(event); err != nil {
			return err
		}
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-sub.C:
			if !ok {
				return errors.New("observer queue full; resubscribe without replaying commands")
			}
			sub.Ack(event)
			if err = emit(event); err != nil {
				return err
			}
		case reply := <-responses:
			if err = write(reply); err != nil {
				return err
			}
		case <-tick.C:
			if err = checkpoint(); err != nil {
				return err
			}
		}
	}
}
