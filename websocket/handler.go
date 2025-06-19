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
	"github.com/shadowofcards/go-toolkit/metrics"
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

// ManagerOption configures the Manager (e.g. inject metrics).
type ManagerOption func(*manager)

// WithManagerMetrics injects a metrics.Recorder into the Manager.
func WithManagerMetrics(rc metrics.Recorder) ManagerOption {
	return func(m *manager) {
		m.metrics = rc
	}
}

// Manager handles connections, rooms and broadcast logic.
type Manager interface {
	Register(ctx context.Context, id string, raw *httpws.Conn) error
	Unregister(ctx context.Context, id string)
	JoinRoom(id, room string)
	LeaveRoom(id, room string)
	SendTo(id string, mt int, msg []byte) error
	SendToRoom(room string, mt int, msg []byte)
	Broadcast(mt int, msg []byte)
	Refresh(ctx context.Context, id string)
	ActiveCount(ctx context.Context) int
}

type manager struct {
	conns   map[string]*SafeConn
	rooms   map[string]map[string]struct{}
	ctxs    map[string]context.Context
	mu      sync.RWMutex
	metrics metrics.Recorder
}

// NewManager creates a new in-memory Manager.
// Optionally inject a metrics.Recorder via WithManagerMetrics.
func NewManager(opts ...ManagerOption) Manager {
	m := &manager{
		conns:   make(map[string]*SafeConn),
		rooms:   make(map[string]map[string]struct{}),
		ctxs:    make(map[string]context.Context),
		metrics: noopRecorder{},
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// noopRecorder implements metrics.Recorder with no effects.
type noopRecorder struct{}

func (noopRecorder) Inc(context.Context, string, int64) error     { return nil }
func (noopRecorder) Gauge(context.Context, string, float64) error { return nil }
func (noopRecorder) IncWithTags(context.Context, string, int64, map[string]string) error {
	return nil
}
func (noopRecorder) GaugeWithTags(context.Context, string, float64, map[string]string) error {
	return nil
}

func (m *manager) Register(ctx context.Context, id string, raw *httpws.Conn) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.conns[id]; ok {
		m.metrics.IncWithTags(ctx, "errors_total", 1, map[string]string{"player_id": id, "stage": "register"})
		return apperr.New().
			WithHTTPStatus(http.StatusConflict).
			WithCode("ALREADY_CONNECTED").
			WithMessage("connection exists")
	}

	m.conns[id] = &SafeConn{Conn: raw}
	m.ctxs[id] = ctx

	m.metrics.GaugeWithTags(ctx, "connections_active", float64(len(m.conns)), map[string]string{"player_id": id})
	return nil
}

func (m *manager) Unregister(ctx context.Context, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if c, ok := m.conns[id]; ok {
		_ = c.Close()
		delete(m.conns, id)
	}
	delete(m.ctxs, id)

	m.metrics.GaugeWithTags(ctx, "connections_active", float64(len(m.conns)), map[string]string{"player_id": id})

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

	if _, ok := m.rooms[room]; !ok {
		m.rooms[room] = make(map[string]struct{})
	}
	m.rooms[room][id] = struct{}{}

	if ctx, ok := m.ctxs[id]; ok {
		m.metrics.IncWithTags(ctx, "room_joins_total", 1, map[string]string{"room": room, "player_id": id})
	}
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

	if ctx, ok := m.ctxs[id]; ok {
		m.metrics.IncWithTags(ctx, "room_leaves_total", 1, map[string]string{"room": room, "player_id": id})
	}
}

func (m *manager) SendTo(id string, mt int, msg []byte) error {
	m.mu.RLock()
	c, ok := m.conns[id]
	ctx := m.ctxs[id]
	m.mu.RUnlock()

	if !ok {
		m.metrics.IncWithTags(ctx, "errors_total", 1, map[string]string{"stage": "send_to", "player_id": id})
		return apperr.New().
			WithHTTPStatus(http.StatusNotFound).
			WithCode("NOT_CONNECTED").
			WithMessage("player not online")
	}

	err := c.WriteMessage(mt, msg)
	if err != nil {
		m.metrics.IncWithTags(ctx, "errors_total", 1, map[string]string{"stage": "write", "player_id": id})
	}
	return err
}

func (m *manager) SendToRoom(room string, mt int, msg []byte) {
	m.mu.RLock()
	set, ok := m.rooms[room]
	m.mu.RUnlock()
	if !ok {
		return
	}

	m.mu.RLock()
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	var ctx context.Context
	if len(ids) > 0 {
		ctx = m.ctxs[ids[0]]
	}

	m.metrics.IncWithTags(ctx, "broadcasts_total", 1, map[string]string{"room": room, "type": "room"})

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

	var ctx context.Context
	if len(ids) > 0 {
		ctx = m.ctxs[ids[0]]
	}

	m.metrics.IncWithTags(ctx, "broadcasts_total", 1, map[string]string{"type": "global"})

	for _, id := range ids {
		_ = m.SendTo(id, mt, msg)
	}
}

func (m *manager) Refresh(ctx context.Context, id string) {
	// no-op or update last-seen timestamp if needed
}

func (m *manager) ActiveCount(ctx context.Context) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.conns)
}

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

type HeartbeatPublisher interface {
	PublishHeartbeat(ctx context.Context, id string) error
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
	metrics            metrics.Recorder
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
func WithMetrics(rc metrics.Recorder) Option { return func(h *Handler) { h.metrics = rc } }

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
		manager:            m,
		handle:             defaultEcho,
		logger:             logger,
		pongWait:           defaultPongWait,
		pingPeriod:         defaultPingPeriod,
		heartbeatPublisher: nil,
		allowedOrigins:     nil,
		metrics:            noopRecorder{},
	}
	for _, o := range opts {
		o(h)
	}
	if _, ok := h.metrics.(noopRecorder); !ok {
		h.handle = h.echoWithMetrics
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

func (h *Handler) Use(mw Middleware) {
	h.middlewares = append(h.middlewares, mw)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	final := func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		pid, _ := ctx.Value(contexts.KeyPlayerID).(string)

		h.metrics.Inc(ctx, "connections_total", 1)
		start := time.Now()

		rawConn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			h.metrics.Inc(ctx, "errors_total", 1)
			h.handleError(ctx, w, err)
			return
		}
		defer rawConn.Close()

		if err := h.manager.Register(ctx, pid, rawConn); err != nil {
			h.metrics.Inc(ctx, "errors_total", 1)
			h.handleError(ctx, w, err)
			return
		}
		h.metrics.Gauge(ctx, "connections_active", float64(h.manager.ActiveCount(ctx)))

		defer func() {
			h.manager.Unregister(ctx, pid)
			h.metrics.Gauge(ctx, "connections_active", float64(h.manager.ActiveCount(ctx)))
			dur := time.Since(start).Milliseconds()
			h.metrics.Gauge(ctx, "connection_duration_ms", float64(dur))
		}()

		conn := &SafeConn{Conn: rawConn}
		conn.SetReadLimit(1 << 20)
		conn.SetReadDeadline(time.Now().Add(h.pongWait))

		var pingTime time.Time
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(h.pongWait))
			h.manager.Refresh(ctx, pid)
			if h.heartbeatPublisher != nil {
				h.heartbeatPublisher.PublishHeartbeat(ctx, pid)
			}
			lat := time.Since(pingTime).Milliseconds()
			h.metrics.Gauge(ctx, "ping_latency_ms", float64(lat))
			return nil
		})

		ticker := time.NewTicker(h.pingPeriod)
		defer ticker.Stop()
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-ticker.C:
					pingTime = time.Now()
					_ = conn.WriteControl(httpws.PingMessage, nil, time.Now().Add(5*time.Second))
				case <-done:
					return
				}
			}
		}()

		h.logger.InfoCtx(ctx, "ws connected", zap.String("player", pid))
		h.handle(ctx, conn)
		close(done)
	}

	handler := final
	for i := len(h.middlewares) - 1; i >= 0; i-- {
		handler = h.middlewares[i](handler)
	}
	handler(w, r)
}

// echoWithMetrics echoes messages and records send/receive counts, sizes, and errors.
func (h *Handler) echoWithMetrics(ctx context.Context, conn *SafeConn) {
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			h.metrics.Inc(ctx, "errors_total", 1)
			return
		}
		h.metrics.Inc(ctx, "messages_received", 1)
		h.metrics.Gauge(ctx, "message_size_bytes", float64(len(msg)))

		if err := conn.WriteMessage(mt, msg); err != nil {
			h.metrics.Inc(ctx, "errors_total", 1)
			return
		}
		h.metrics.Inc(ctx, "messages_sent", 1)
		h.metrics.Gauge(ctx, "message_size_bytes", float64(len(msg)))
	}
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

func (h *Handler) handleError(ctx context.Context, w http.ResponseWriter, err error) {
	var payload errorPayload
	status := http.StatusInternalServerError

	if ae, ok := apperr.FromError(err); ok {
		payload = errorPayload{Code: ae.ErrCode(), Message: ae.Message, Context: ae.Context}
		status = ae.Status()
		h.logger.WarnCtx(ctx, "ws app error", zap.String("code", payload.Code), zap.Error(err))
	} else {
		payload = errorPayload{Code: "INTERNAL_ERROR", Message: "internal server error"}
		h.logger.ErrorCtx(ctx, "ws internal error", zap.Error(err))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": payload})
}
