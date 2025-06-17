package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	httpws "github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/shadowofcards/go-toolkit/contexts"
	apperr "github.com/shadowofcards/go-toolkit/errors"
	"github.com/shadowofcards/go-toolkit/logging"
)

/*──────────────────────────────
   CONNECTION WRAPPER
──────────────────────────────*/

type SafeConn struct {
	*httpws.Conn
	mu sync.Mutex
}

func (c *SafeConn) WriteMessage(mt int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteMessage(mt, data)
}

func (c *SafeConn) WriteControl(mt int, data []byte, deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteControl(mt, data, deadline)
}

/*──────────────────────────────
   MANAGER
──────────────────────────────*/

type Manager interface {
	Register(ctx context.Context, id string, raw *httpws.Conn) error
	Unregister(ctx context.Context, id string)
	JoinRoom(id, room string)
	LeaveRoom(id, room string)
	SendTo(id string, mt int, msg []byte) error
	SendToRoom(room string, mt int, msg []byte)
	Broadcast(mt int, msg []byte)
	Refresh(ctx context.Context, id string)
}

type manager struct {
	conns map[string]*SafeConn
	rooms map[string]map[string]struct{}
	mu    sync.RWMutex
}

func NewManager() Manager {
	return &manager{
		conns: make(map[string]*SafeConn),
		rooms: make(map[string]map[string]struct{}),
	}
}

func (m *manager) Register(_ context.Context, id string, raw *httpws.Conn) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conns[id]; ok {
		return apperr.New().WithHTTPStatus(http.StatusConflict).WithCode("ALREADY_CONNECTED").WithMessage("connection exists")
	}
	m.conns[id] = &SafeConn{Conn: raw}
	return nil
}

func (m *manager) Unregister(_ context.Context, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.conns[id]; ok {
		_ = c.Close()
		delete(m.conns, id)
	}
	for room, set := range m.rooms {
		delete(set, id)
		if len(set) == 0 {
			delete(m.rooms, room)
		}
	}
}

func (m *manager) JoinRoom(id, room string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set, ok := m.rooms[room]
	if !ok {
		set = make(map[string]struct{})
		m.rooms[room] = set
	}
	set[id] = struct{}{}
}

func (m *manager) LeaveRoom(id, room string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if set, ok := m.rooms[room]; ok {
		delete(set, id)
		if len(set) == 0 {
			delete(m.rooms, room)
		}
	}
}

func (m *manager) SendTo(id string, mt int, msg []byte) error {
	m.mu.RLock()
	c, ok := m.conns[id]
	m.mu.RUnlock()
	if !ok {
		return apperr.New().WithHTTPStatus(http.StatusNotFound).WithCode("NOT_CONNECTED").WithMessage("player not online")
	}
	return c.WriteMessage(mt, msg)
}

func (m *manager) SendToRoom(room string, mt int, msg []byte) {
	m.mu.RLock()
	set, ok := m.rooms[room]
	if !ok {
		m.mu.RUnlock()
		return
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		_ = m.SendTo(id, mt, msg)
	}
}

func (m *manager) Broadcast(mt int, msg []byte) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.conns))
	for id := range m.conns {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		_ = m.SendTo(id, mt, msg)
	}
}

func (m *manager) Refresh(_ context.Context, _ string) {}

/*──────────────────────────────
   HANDLER
──────────────────────────────*/

type Middleware func(next http.HandlerFunc) http.HandlerFunc
type HandlerFunc func(ctx context.Context, conn *SafeConn)

type errorPayload struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Context interface{} `json:"context,omitempty"`
}

type Handler struct {
	upgrader           httpws.Upgrader
	manager            Manager
	middlewares        []Middleware
	handle             HandlerFunc
	logger             *logging.Logger
	heartbeatPublisher HeartbeatPublisher
	pongWait           time.Duration
	pingPeriod         time.Duration
	allowedOrigins     []string
}

type HeartbeatPublisher interface {
	PublishHeartbeat(ctx context.Context, id string) error
}

type Option func(*Handler)

func WithUpgrader(u httpws.Upgrader) Option { return func(h *Handler) { h.upgrader = u } }
func WithHandlerFunc(fn HandlerFunc) Option { return func(h *Handler) { h.handle = fn } }
func WithLogger(l *logging.Logger) Option   { return func(h *Handler) { h.logger = l } }
func WithPingPong(pw, pp time.Duration) Option {
	return func(h *Handler) { h.pongWait, h.pingPeriod = pw, pp }
}
func WithHeartbeatPublisher(p HeartbeatPublisher) Option {
	return func(h *Handler) { h.heartbeatPublisher = p }
}
func WithAllowedOrigins(origins ...string) Option {
	return func(h *Handler) { h.allowedOrigins = origins }
}

const (
	defaultPongWait   = 60 * time.Second
	defaultPingPeriod = (defaultPongWait * 9) / 10
)

func NewHandler(m Manager, opts ...Option) *Handler {
	logger, err := logging.New()
	if err != nil {
		panic("logger init failed: " + err.Error())
	}
	h := &Handler{
		upgrader: httpws.Upgrader{
			ReadBufferSize:    4096,
			WriteBufferSize:   4096,
			EnableCompression: false,
		},
		manager:    m,
		handle:     defaultEcho,
		logger:     logger,
		pongWait:   defaultPongWait,
		pingPeriod: defaultPingPeriod,
	}
	for _, o := range opts {
		o(h)
	}
	h.upgrader.CheckOrigin = func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if len(h.allowedOrigins) > 0 {
			for _, a := range h.allowedOrigins {
				if a == origin {
					return true
				}
			}
			return false
		}
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	}
	return h
}

func defaultEcho(ctx context.Context, conn *SafeConn) {
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteMessage(mt, msg)
	}
}

func (h *Handler) Use(mw Middleware) { h.middlewares = append(h.middlewares, mw) }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	final := func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		tid, _ := ctx.Value(contexts.KeyTenantID).(string)
		pid, _ := ctx.Value(contexts.KeyPlayerID).(string)

		rawConn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			h.handleError(ctx, w, err)
			return
		}
		defer rawConn.Close()

		conn := &SafeConn{Conn: rawConn}
		conn.SetReadLimit(1 << 20)
		conn.SetReadDeadline(time.Now().UTC().Add(h.pongWait))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().UTC().Add(h.pongWait))
			h.manager.Refresh(ctx, pid)
			if h.heartbeatPublisher != nil {
				_ = h.heartbeatPublisher.PublishHeartbeat(ctx, pid)
			}
			return nil
		})

		if err := h.manager.Register(ctx, pid, rawConn); err != nil {
			h.handleError(ctx, w, err)
			return
		}
		defer h.manager.Unregister(ctx, pid)

		ticker := time.NewTicker(h.pingPeriod)
		defer ticker.Stop()
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-ticker.C:
					_ = conn.WriteControl(httpws.PingMessage, nil, time.Now().UTC().Add(5*time.Second))
				case <-done:
					return
				}
			}
		}()

		h.logger.InfoCtx(ctx, "ws connected", zap.String("tenant", tid), zap.String("player", pid))
		h.handle(ctx, conn)
		close(done)
	}

	handler := final
	for i := len(h.middlewares) - 1; i >= 0; i-- {
		handler = h.middlewares[i](handler)
	}
	handler(w, r)
}

func (h *Handler) handleError(ctx context.Context, w http.ResponseWriter, err error) {
	var payload errorPayload
	var status int

	if ae, ok := apperr.FromError(err); ok {
		payload = errorPayload{Code: ae.ErrCode(), Message: ae.Message, Context: ae.Context}
		status = ae.Status()
		h.logger.WarnCtx(ctx, "ws app error", zap.String("code", payload.Code), zap.Error(err))
	} else {
		payload = errorPayload{Code: "INTERNAL_ERROR", Message: "internal server error"}
		status = http.StatusInternalServerError
		h.logger.ErrorCtx(ctx, "ws internal error", zap.Error(err))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": payload})
}
