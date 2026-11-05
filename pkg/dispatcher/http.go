package dispatcher

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	iterlog "github.com/arketov/iterion/pkg/log"
)

const (
	// wsWriteWait caps a single WriteMessage. A stuck client cannot
	// hold the writer goroutine forever; the connection is dropped
	// instead.
	wsWriteWait = 10 * time.Second
	// wsPongWait is the longest the reader will accept silence from
	// the client. The pong handler resets it on each pong frame.
	wsPongWait = 60 * time.Second
	// wsPingPeriod is the cadence at which the writer sends a ping.
	// Smaller than pongWait so the client always has time to respond.
	wsPingPeriod = (wsPongWait * 9) / 10
	// wsReadLimit caps a single inbound message size. Dispatcher
	// clients don't speak — anything over this is a misuse and the
	// reader drops the connection.
	wsReadLimit = 1 << 16
)

// Routes returns an http.Handler exposing the dispatcher's REST + WS
// surface. Mount it under a prefix like "/api/v1/dispatcher".
func (c *Dispatcher) Routes() http.Handler {
	mux := http.NewServeMux()
	c.RegisterRoutes(mux, "")
	return mux
}

// RegisterRoutes registers the dispatcher's HTTP handlers on the given
// mux under the supplied prefix. Pass "" to mount at the mux root.
// Method-specific patterns are used so registration coexists with
// other method+path routes (e.g. the studio server's CORS preflight).
func (c *Dispatcher) RegisterRoutes(mux *http.ServeMux, prefix string) {
	c.RegisterRoutesWithMiddleware(mux, prefix, nil)
}

// RegisterRoutesWithMiddleware mounts the routes through a caller-
// supplied wrapper (typically the studio server's requireAuth). See
// native.Store.RegisterRoutesWithMiddleware for the rationale.
func (c *Dispatcher) RegisterRoutesWithMiddleware(mux *http.ServeMux, prefix string, wrap func(http.Handler) http.Handler) {
	p := strings.TrimSuffix(prefix, "/")
	if wrap == nil {
		wrap = func(h http.Handler) http.Handler { return h }
	}
	mux.Handle("GET "+p+"/state", wrap(http.HandlerFunc(c.handleState)))
	mux.Handle("POST "+p+"/refresh", wrap(http.HandlerFunc(c.handleRefresh)))
	mux.Handle("POST "+p+"/reload", wrap(http.HandlerFunc(c.handleReload)))
	mux.Handle("GET "+p+"/issues/{id}", wrap(http.HandlerFunc(c.handleIssueDetail)))
	mux.Handle("POST "+p+"/issues/{id}/cancel", wrap(http.HandlerFunc(c.handleIssueCancel)))
	mux.Handle("GET "+p+"/ws", wrap(http.HandlerFunc(c.handleWS)))
}

func (c *Dispatcher) handleState(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, c.Snapshot())
}

func (c *Dispatcher) handleRefresh(w http.ResponseWriter, _ *http.Request) {
	c.Refresh()
	WriteJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

func (c *Dispatcher) handleReload(w http.ResponseWriter, _ *http.Request) {
	cfg, err := Load(c.cfg.Load().SourcePath)
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.Reload(cfg)
	WriteJSON(w, http.StatusOK, map[string]any{"reloaded": true, "polling_interval_s": cfg.PollingInterval().Seconds()})
}

func (c *Dispatcher) handleIssueDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap := c.Snapshot()
	for _, r := range snap.Running {
		if r.IssueID == id {
			WriteJSON(w, http.StatusOK, r)
			return
		}
	}
	for _, r := range snap.Retries {
		if r.IssueID == id {
			WriteJSON(w, http.StatusOK, r)
			return
		}
	}
	http.Error(w, "issue not tracked by dispatcher", http.StatusNotFound)
}

func (c *Dispatcher) handleIssueCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c.Cancel(id)
	WriteJSON(w, http.StatusAccepted, map[string]string{"issue_id": id, "status": "cancel_requested"})
}

// ---------------------------------------------------------------------------
// WebSocket fan-out
// ---------------------------------------------------------------------------

// dispatcherUpgrader is permissive — the dispatcher binds to localhost
// by default and the operator chooses when to expose it. Operators in
// hostile environments should run the dispatcher behind a reverse proxy
// that enforces origin policy.
var dispatcherUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

func (c *Dispatcher) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := dispatcherUpgrader.Upgrade(w, r, nil)
	if err != nil {
		c.logger.Warn("dispatcher: ws upgrade: %v", err)
		return
	}
	// Push the current snapshot immediately so a fresh subscriber
	// gets state without waiting for the next tick.
	c.ws.attach(conn, c.Snapshot())
}

// wsClientConn wraps a single websocket subscription.
type wsClientConn struct {
	conn *websocket.Conn
	send chan []byte
}

// wsBridge fans Snapshot publications out to every connected client.
// It is also responsible for protocol-level keepalive (ping/pong) and
// for forcibly dropping slow or unreachable clients via write
// deadlines, so the dispatcher never leaks goroutines waiting on a
// dead network peer.
type wsBridge struct {
	mu      sync.Mutex
	clients map[*wsClientConn]struct{}
	closed  bool

	stopOnce sync.Once
	stop     chan struct{}
	// logger is used for panic recovery in the per-client writer/reader
	// goroutines; non-nil so callers can rely on it without a nil guard.
	logger *iterlog.Logger
}

func newWsBridge(logger *iterlog.Logger) *wsBridge {
	if logger == nil {
		logger = iterlog.New(iterlog.LevelInfo, io.Discard)
	}
	return &wsBridge{
		clients: map[*wsClientConn]struct{}{},
		stop:    make(chan struct{}),
		logger:  logger,
	}
}

// Stop closes every connected client and prevents new ones from
// attaching. Idempotent.
func (b *wsBridge) Stop() {
	b.stopOnce.Do(func() {
		close(b.stop)
		b.mu.Lock()
		b.closed = true
		for c := range b.clients {
			_ = c.conn.Close()
		}
		b.mu.Unlock()
	})
}

func (b *wsBridge) attach(conn *websocket.Conn, initial Snapshot) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = conn.Close()
		return
	}
	client := &wsClientConn{
		conn: conn,
		send: make(chan []byte, 16),
	}
	b.clients[client] = struct{}{}
	b.mu.Unlock()

	if data, err := json.Marshal(initial); err == nil {
		select {
		case client.send <- data:
		default:
		}
	}

	go client.writer(b)
	client.reader(b)
}

func (b *wsBridge) drop(client *wsClientConn) {
	b.mu.Lock()
	if _, ok := b.clients[client]; ok {
		delete(b.clients, client)
		close(client.send)
	}
	b.mu.Unlock()
	_ = client.conn.Close()
}

func (b *wsBridge) broadcast(s Snapshot) {
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for client := range b.clients {
		select {
		case client.send <- data:
		default:
			// slow consumer: drop the message; the writer's deadline
			// will kick them off if they stay unresponsive.
		}
	}
}

// writer drains the send channel and emits a periodic ping so the
// TCP layer notices a dead peer instead of silently leaking.
func (c *wsClientConn) writer(b *wsBridge) {
	defer func() {
		if r := recover(); r != nil {
			// A panic in the writer would otherwise propagate up and
			// crash the dispatcher process; the connection is going
			// away anyway, so log and let the deferred drop close it.
			b.logger.Error("dispatcher: ws writer panic: %v", r)
		}
	}()
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		b.drop(c)
	}()
	for {
		select {
		case data, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-b.stop:
			return
		}
	}
}

// reader keeps the connection alive by enforcing a pong-driven read
// deadline. The dispatcher doesn't accept client-side messages — any
// read that fails (close, pong timeout, frame too large) drops the
// connection.
func (c *wsClientConn) reader(b *wsBridge) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("dispatcher: ws reader panic: %v", r)
		}
	}()
	defer b.drop(c)
	c.conn.SetReadLimit(wsReadLimit)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) &&
				!errors.Is(err, websocket.ErrCloseSent) {
				return
			}
			return
		}
	}
}
