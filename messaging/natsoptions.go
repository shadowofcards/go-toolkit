package messaging

import (
	"time"

	"github.com/nats-io/nats.go"

	"github.com/leandrodaf/go-toolkit/logging"
)

type Option func(*cfg)

func WithURL(u string) Option                  { return func(c *cfg) { c.url = u } }
func WithName(n string) Option                 { return func(c *cfg) { c.name = n } }
func WithMaxReconnects(n int) Option           { return func(c *cfg) { c.maxReconnects = n } }
func WithReconnectWait(d time.Duration) Option { return func(c *cfg) { c.reconnectWait = d } }
func WithTimeout(d time.Duration) Option       { return func(c *cfg) { c.timeout = d } }
func WithLogger(l *logging.Logger) Option      { return func(c *cfg) { c.logger = l } }
func WithoutDrainOnStop() Option               { return func(c *cfg) { c.drainOnStop = false } }
func WithNATSOptions(opts ...nats.Option) Option {
	return func(c *cfg) { c.customOptions = append(c.customOptions, opts...) }
}
