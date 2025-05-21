package messaging

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/shadowofcards/go-toolkit/logging"
)

type cfg struct {
	url           string
	name          string
	maxReconnects int
	reconnectWait time.Duration
	timeout       time.Duration
	logger        *logging.Logger
	drainOnStop   bool
	customOptions []nats.Option
}

func defaultCfg() *cfg {
	return &cfg{
		url:           nats.DefaultURL,
		name:          "nats-client",
		maxReconnects: -1,
		reconnectWait: 5 * time.Second,
		timeout:       10 * time.Second,
		drainOnStop:   true,
	}
}

func Connect(opts ...Option) (*nats.Conn, error) {
	c := defaultCfg()
	for _, opt := range opts {
		opt(c)
	}

	var nopts []nats.Option
	nopts = append(nopts,
		nats.Name(c.name),
		nats.MaxReconnects(c.maxReconnects),
		nats.ReconnectWait(c.reconnectWait),
		nats.Timeout(c.timeout),
	)
	if c.logger != nil {
		nopts = append(nopts,
			nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
				c.logger.Warn("nats disconnected", zap.Error(err))
			}),
			nats.ReconnectHandler(func(nc *nats.Conn) {
				c.logger.Info("nats reconnected", zap.String("url", nc.ConnectedUrl()))
			}),
			nats.ClosedHandler(func(_ *nats.Conn) {
				c.logger.Warn("nats connection closed")
			}),
		)
	}

	nopts = append(nopts, c.customOptions...)

	conn, err := nats.Connect(c.url, nopts...)
	if err != nil {
		return nil, err
	}
	if c.logger != nil {
		c.logger.Info("nats connected", zap.String("url", conn.ConnectedUrl()))
	}
	return conn, nil
}

type Params struct {
	fx.In
	Lifecycle fx.Lifecycle
	Options   []Option `group:"nats_options"`
}

func ProvideConn(p Params) (*nats.Conn, error) {
	conn, err := Connect(p.Options...)
	if err != nil {
		return nil, err
	}
	p.Lifecycle.Append(fx.Hook{
		OnStop: func(_ context.Context) error {
			if pconn := conn; pconn != nil {
				if hasDrain(p.Options) {
					_ = pconn.Drain()
				}
				pconn.Close()
			}
			return nil
		},
	})
	return conn, nil
}

func hasDrain(opts []Option) bool {
	c := defaultCfg()
	for _, o := range opts {
		o(c)
	}
	return c.drainOnStop
}
