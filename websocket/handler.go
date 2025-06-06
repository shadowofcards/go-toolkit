package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shadowofcards/go-toolkit/contexts"
	apperr "github.com/shadowofcards/go-toolkit/errors"
	"github.com/shadowofcards/go-toolkit/logging"
	"go.uber.org/zap"
)

// errorPayload mirrors the Fiber JSON error payload.
type errorPayload struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Context interface{} `json:"context,omitempty"`
}

// Manager knows how to register/unregister connections and refresh sessions.
type Manager interface {
	Register(ctx context.Context, id string, conn *websocket.Conn) error
	Unregister(ctx context.Context, id string) error
	SendTo(id string, mt int, msg []byte) error
	Broadcast(mt int, msg []byte)
	Refresh(ctx context.Context, id string)
}

// HeartbeatPublisher defines how to publish a heartbeat event (e.g., to NATS).
type HeartbeatPublisher interface {
	PublishHeartbeat(ctx context.Context, id string) error
}

// Middleware wraps an http.HandlerFunc.
type Middleware func(next http.HandlerFunc) http.HandlerFunc

// HandlerFunc is invoked once the WebSocket is established.
type HandlerFunc func(ctx context.Context, conn *websocket.Conn)

// Handler is the generic WebSocket HTTP handler.
type Handler struct {
	upgrader           websocket.Upgrader
	manager            Manager
	middlewares        []Middleware
	handle             HandlerFunc
	logger             *logging.Logger
	heartbeatPublisher HeartbeatPublisher

	// heartbeat timeouts
	pongWait   time.Duration
	pingPeriod time.Duration
}

// Option customizes a Handler.
type Option func(*Handler)

// WithUpgrader overrides the default upgrader.
func WithUpgrader(u websocket.Upgrader) Option {
	return func(h *Handler) { h.upgrader = u }
}

// WithHandlerFunc sets the core message loop.
func WithHandlerFunc(fn HandlerFunc) Option {
	return func(h *Handler) { h.handle = fn }
}

// WithLogger injects a go-toolkit Logger for internal logs.
func WithLogger(log *logging.Logger) Option {
	return func(h *Handler) { h.logger = log }
}

// WithPingPong overrides the default ping/pong timings.
func WithPingPong(pongWait, pingPeriod time.Duration) Option {
	return func(h *Handler) {
		h.pongWait = pongWait
		h.pingPeriod = pingPeriod
	}
}

// WithHeartbeatPublisher sets a HeartbeatPublisher to emit heartbeat events.
func WithHeartbeatPublisher(p HeartbeatPublisher) Option {
	return func(h *Handler) { h.heartbeatPublisher = p }
}

// Default timing for ping/pong to keep session alive.
const (
	defaultPongWait   = 60 * time.Second
	defaultPingPeriod = (defaultPongWait * 9) / 10
)

// NewHandler builds a Handler with defaults (echo loop, permissive upgrader).
func NewHandler(m Manager, opts ...Option) *Handler {
	logger, err := logging.New()
	if err != nil {
		panic("failed to create logger: " + err.Error())
	}

	h := &Handler{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
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
	return h
}

// defaultEcho is the default HandlerFunc: echo back messages.
func defaultEcho(ctx context.Context, conn *websocket.Conn) {
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := conn.WriteMessage(mt, msg); err != nil {
			return
		}
	}
}

// Use appends a Middleware.
func (h *Handler) Use(mw Middleware) {
	h.middlewares = append(h.middlewares, mw)
}

// closeConnection envia o frame de Close e fecha a conexão
func (h *Handler) closeConnection(conn *websocket.Conn, code int, text string) {
	// tenta enviar um CloseFrame com timeout de 5s
	if err := conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, text),
		time.Now().Add(5*time.Second),
	); err != nil {
		h.logger.Warn("failed to send close frame", zap.Error(err))
	}
	conn.Close()
}

// ServeHTTP implementa http.Handler com heartbeat "server-pull" e clean close
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	final := func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		id, _ := ctx.Value(contexts.KeyPlayerID).(string)

		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			h.handleError(ctx, w, r, err)
			return
		}
		// clean close via helper
		defer h.closeConnection(conn, websocket.CloseNormalClosure, "server shutdown")

		h.logger.InfoCtx(ctx, "WebSocket handshake", zap.String("playerID", id))

		if err := h.manager.Register(ctx, id, conn); err != nil {
			h.handleError(ctx, w, r, err)
			return
		}
		defer h.manager.Unregister(ctx, id)

		// HEARTBEAT "SERVER-PULL"
		ticker := time.NewTicker(h.pingPeriod)
		defer ticker.Stop()

		go func() {
			for {
				select {
				case <-ticker.C:
					// ping de controle; falha = desconexão
					if err := conn.WriteControl(
						websocket.PingMessage,
						nil,
						time.Now().Add(5*time.Second),
					); err != nil {
						cancel()
						return
					}
					h.manager.Refresh(ctx, id)

					if h.heartbeatPublisher != nil {
						go func() {
							if err := h.heartbeatPublisher.PublishHeartbeat(ctx, id); err != nil {
								h.logger.WarnCtx(ctx, "heartbeat publish failed", zap.Error(err))
							}
						}()
					}

				case <-ctx.Done():
					return
				}
			}
		}()

		h.handle(ctx, conn)
	}

	handler := final
	for i := len(h.middlewares) - 1; i >= 0; i-- {
		handler = h.middlewares[i](handler)
	}
	handler(w, r)
}

// handleError standardizes error responses using go-toolkit/errors.AppError.
func (h *Handler) handleError(ctx context.Context, w http.ResponseWriter, _ *http.Request, err error) {
	var (
		payload errorPayload
		status  int
	)
	if ae, ok := apperr.FromError(err); ok {
		payload = errorPayload{Code: ae.ErrCode(), Message: ae.Message, Context: ae.Context}
		status = ae.Status()
		h.logger.WarnCtx(ctx, "WebSocket app error", zap.String("code", payload.Code), zap.Error(err))
	} else {
		payload = errorPayload{Code: "INTERNAL_ERROR", Message: "internal server error"}
		status = http.StatusInternalServerError
		h.logger.ErrorCtx(ctx, "WebSocket internal error", zap.Error(err))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": payload})
}
