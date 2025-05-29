// go-toolkit/websocket/handler.go
package websocket

import (
	"context"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/shadowofcards/go-toolkit/contexts"
)

// Manager knows how to register/unregister connections.
type Manager interface {
	Register(ctx context.Context, id string, conn *websocket.Conn) error
	Unregister(ctx context.Context, id string) error
	SendTo(id string, mt int, msg []byte) error
	Broadcast(mt int, msg []byte)
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
}

// Option customizes a Handler.
type Option func(*Handler)

// WithUpgrader overrides the default upgrader.
func WithUpgrader(u websocket.Upgrader) Option {
	return func(h *Handler) {
		h.upgrader = u
	}
}

// WithHandlerFunc sets the core message loop.
func WithHandlerFunc(fn HandlerFunc) Option {
	return func(h *Handler) {
		h.handle = fn
	}
}

// NewHandler builds a Handler with defaults (echo).
func NewHandler(m Manager, opts ...Option) *Handler {
	h := &Handler{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		manager: m,
		handle: func(ctx context.Context, conn *websocket.Conn) {
			for {
				mt, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if err := conn.WriteMessage(mt, msg); err != nil {
					return
				}
			}
		},
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Use appends a Middleware.
func (h *Handler) Use(mw Middleware) {
	h.middlewares = append(h.middlewares, mw)
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// extract ID from context (e.g. playerID)
	id, _ := ctx.Value(contexts.KeyPlayerID).(string)

	final := func(w http.ResponseWriter, r *http.Request) {
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = h.manager.Register(ctx, id, conn)
		defer h.manager.Unregister(ctx, id)

		h.handle(ctx, conn)
	}

	// chain middlewares
	fn := final
	for i := len(h.middlewares) - 1; i >= 0; i-- {
		fn = h.middlewares[i](fn)
	}
	fn(w, r)
}
