package websocket

import (
	"time"

	httpws "github.com/gorilla/websocket"
	"github.com/shadowofcards/go-toolkit/logging"
	"github.com/shadowofcards/go-toolkit/metrics"
)

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
