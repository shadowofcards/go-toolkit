package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	httpws "github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/shadowofcards/go-toolkit/contexts"
	apperr "github.com/shadowofcards/go-toolkit/errors"
	"github.com/shadowofcards/go-toolkit/logging"
	"github.com/shadowofcards/go-toolkit/metrics"
)

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
		metrics:            nil,
	}
	for _, o := range opts {
		o(h)
	}
	if h.metrics != nil {
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

		if h.metrics != nil {
			h.metrics.Inc(ctx, "connections_total", 1)
		}
		start := time.Now()

		rawConn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			if h.metrics != nil {
				h.metrics.Inc(ctx, "errors_total", 1)
			}
			h.handleError(ctx, w, err)
			return
		}
		defer rawConn.Close()

		if err := h.manager.Register(ctx, pid, rawConn); err != nil {
			if h.metrics != nil {
				h.metrics.Inc(ctx, "errors_total", 1)
			}
			h.handleError(ctx, w, err)
			return
		}
		if h.metrics != nil {
			h.metrics.Gauge(ctx, "connections_active", float64(h.manager.ActiveCount(ctx)))
		}

		defer func() {
			h.manager.Unregister(ctx, pid)
			if h.metrics != nil {
				h.metrics.Gauge(ctx, "connections_active", float64(h.manager.ActiveCount(ctx)))
				dur := time.Since(start).Milliseconds()
				h.metrics.Gauge(ctx, "connection_duration_ms", float64(dur))
			}
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
			if h.metrics != nil {
				lat := time.Since(pingTime).Milliseconds()
				h.metrics.Gauge(ctx, "ping_latency_ms", float64(lat))
			}
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

func (h *Handler) echoWithMetrics(ctx context.Context, conn *SafeConn) {
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			if h.metrics != nil {
				h.metrics.Inc(ctx, "errors_total", 1)
			}
			return
		}
		if h.metrics != nil {
			h.metrics.Inc(ctx, "messages_received", 1)
			h.metrics.Gauge(ctx, "message_size_bytes", float64(len(msg)))
		}

		if err := conn.WriteMessage(mt, msg); err != nil {
			if h.metrics != nil {
				h.metrics.Inc(ctx, "errors_total", 1)
			}
			return
		}
		if h.metrics != nil {
			h.metrics.Inc(ctx, "messages_sent", 1)
			h.metrics.Gauge(ctx, "message_size_bytes", float64(len(msg)))
		}
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
