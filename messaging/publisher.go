package messaging

import (
	"context"
	"encoding/json"
	"time"

	"github.com/leandrodaf/go-toolkit/logging"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

type Publisher struct {
	conn   *nats.Conn
	log    *logging.Logger
	prefix string
}

type OptionPublisher func(*Publisher)

func WithPrefix(pf string) OptionPublisher { return func(p *Publisher) { p.prefix = pf } }

func NewPublisher(nc *nats.Conn, log *logging.Logger, opts ...OptionPublisher) *Publisher {
	p := &Publisher{conn: nc, log: log}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Publisher) Publish(ctx context.Context, subject string, msg any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if p.prefix != "" {
		subject = p.prefix + subject
	}
	data, err := json.Marshal(msg)
	if err != nil {
		p.log.ErrorCtx(ctx, "failed to marshal message", zap.String("subject", subject), zap.Error(err))
		return err
	}
	if err := p.conn.Publish(subject, data); err != nil {
		p.log.ErrorCtx(ctx, "failed to publish message", zap.String("subject", subject), zap.Error(err))
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		if err := p.conn.FlushTimeout(time.Until(dl)); err != nil {
			p.log.ErrorCtx(ctx, "flush timeout", zap.String("subject", subject), zap.Error(err))
			return err
		}
	} else if err := p.conn.Flush(); err != nil {
		p.log.ErrorCtx(ctx, "flush failed", zap.String("subject", subject), zap.Error(err))
		return err
	}
	p.log.DebugCtx(ctx, "message published", zap.String("subject", subject))
	return nil
}
