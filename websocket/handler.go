// go-toolkit/websocket/handler.go
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

// Middleware wraps an http.HandlerFunc.
type Middleware func(next http.HandlerFunc) http.HandlerFunc

// HandlerFunc is invoked once the WebSocket is established.
type HandlerFunc func(ctx context.Context, conn *websocket.Conn)

// Handler is the generic WebSocket HTTP handler.
type Handler struct {
	upgrader    websocket.Upgrader
	manager     Manager
	middlewares []Middleware
	handle      HandlerFunc
	logger      *logging.Logger

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

// ServeHTTP implements http.Handler with context propagation, middleware chain,
// configurable heartbeat, and standardized error handling.
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
		defer conn.Close()
		h.logger.InfoCtx(ctx, "WebSocket handshake", zap.String("playerID", id))

		if err := h.manager.Register(ctx, id, conn); err != nil {
			h.handleError(ctx, w, r, err)
			return
		}
		defer h.manager.Unregister(ctx, id)

		// setup heartbeat
		conn.SetReadDeadline(time.Now().Add(h.pongWait))
		conn.SetPongHandler(func(string) error {
			h.manager.Refresh(ctx, id)
			conn.SetReadDeadline(time.Now().Add(h.pongWait))
			return nil
		})

		// launch ping routine bound to ctx
		ticker := time.NewTicker(h.pingPeriod)
		defer ticker.Stop()
		go func() {
			for {
				select {
				case <-ticker.C:
					if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
						cancel()
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()

		// invoke business logic
		h.handle(ctx, conn)
	}

	// apply middlewares
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
