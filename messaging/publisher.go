package messaging

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/xid"
	"github.com/shadowofcards/go-toolkit/logging"
	"github.com/shadowofcards/go-toolkit/metrics"
	"go.uber.org/zap"
)

type Publisher struct {
	conn         *nats.Conn
	js           nats.JetStreamContext
	log          *logging.Logger
	prefix       string
	metrics      metrics.Recorder
	useJetStream bool
}

type OptionPublisher func(*Publisher)

func WithPrefix(pf string) OptionPublisher           { return func(p *Publisher) { p.prefix = pf } }
func WithMetrics(m metrics.Recorder) OptionPublisher { return func(p *Publisher) { p.metrics = m } }
func WithJetStream(enabled bool) OptionPublisher {
	return func(p *Publisher) { p.useJetStream = enabled }
}

func NewPublisher(nc *nats.Conn, log *logging.Logger, opts ...OptionPublisher) *Publisher {
	var js nats.JetStreamContext
	if jsCtx, err := nc.JetStream(); err == nil {
		js = jsCtx
	}
	p := &Publisher{
		conn: nc,
		js:   js,
		log:  log,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Publisher) EnsureStream(subject string) error {
	if p.js == nil {
		return nil
	}
	_, err := p.js.StreamInfo(subject)
	if err == nats.ErrStreamNotFound {
		_, err = p.js.AddStream(&nats.StreamConfig{
			Name:     subject,
			Subjects: []string{subject},
		})
	}
	return err
}

// Publish faz publish com suporte a JetStream deduplicado (Msg-Id) e métricas.
func (p *Publisher) Publish(ctx context.Context, subject string, msg any) error {
	return p.PublishWithID(ctx, subject, msg, "")
}

// PublishWithID permite informar o Msg-Id manualmente (útil para deduplicação explícita).
func (p *Publisher) PublishWithID(ctx context.Context, subject string, msg any, msgID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if p.prefix != "" {
		subject = p.prefix + subject
	}
	if p.useJetStream && p.js != nil {
		if msgID == "" {
			msgID = xid.New().String()
		}
		if err := p.EnsureStream(subject); err != nil {
			return err
		}
		var start time.Time
		if p.metrics != nil {
			start = time.Now()
		}
		tags := map[string]string{"subject": subject}
		data, err := json.Marshal(msg)
		if err != nil {
			tags["status"] = "marshal_error"
			if p.metrics != nil {
				p.metrics.IncWithTags(ctx, "nats_publish_total", 1, tags)
			}
			p.log.ErrorCtx(ctx, "failed to marshal message", zap.String("subject", subject), zap.Error(err))
			return err
		}
		_, err = p.js.PublishMsg(&nats.Msg{
			Subject: subject,
			Data:    data,
			Header:  nats.Header{"Nats-Msg-Id": []string{msgID}},
		})
		if err != nil {
			tags["status"] = "publish_error"
			if p.metrics != nil {
				p.metrics.IncWithTags(ctx, "nats_publish_total", 1, tags)
			}
			p.log.ErrorCtx(ctx, "failed to publish JetStream message", zap.String("subject", subject), zap.Error(err))
			return err
		}
		tags["status"] = "success"
		if p.metrics != nil {
			p.metrics.IncWithTags(ctx, "nats_publish_total", 1, tags)
			if !start.IsZero() {
				p.metrics.ObserveWithTags(ctx, "nats_publish_duration_seconds", time.Since(start).Seconds(), tags)
			}
		}
		p.log.DebugCtx(ctx, "JetStream message published", zap.String("subject", subject))
		return nil
	}

	// Fallback para NATS core/clássico
	var start time.Time
	if p.metrics != nil {
		start = time.Now()
	}
	tags := map[string]string{"subject": subject}
	data, err := json.Marshal(msg)
	if err != nil {
		tags["status"] = "marshal_error"
		if p.metrics != nil {
			p.metrics.IncWithTags(ctx, "nats_publish_total", 1, tags)
		}
		p.log.ErrorCtx(ctx, "failed to marshal message", zap.String("subject", subject), zap.Error(err))
		return err
	}
	if err := p.conn.Publish(subject, data); err != nil {
		tags["status"] = "publish_error"
		if p.metrics != nil {
			p.metrics.IncWithTags(ctx, "nats_publish_total", 1, tags)
		}
		p.log.ErrorCtx(ctx, "failed to publish message", zap.String("subject", subject), zap.Error(err))
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		if err := p.conn.FlushTimeout(time.Until(dl)); err != nil {
			tags["status"] = "flush_error"
			if p.metrics != nil {
				p.metrics.IncWithTags(ctx, "nats_publish_total", 1, tags)
			}
			p.log.ErrorCtx(ctx, "flush timeout", zap.String("subject", subject), zap.Error(err))
			return err
		}
	} else if err := p.conn.Flush(); err != nil {
		tags["status"] = "flush_error"
		if p.metrics != nil {
			p.metrics.IncWithTags(ctx, "nats_publish_total", 1, tags)
		}
		p.log.ErrorCtx(ctx, "flush failed", zap.String("subject", subject), zap.Error(err))
		return err
	}
	tags["status"] = "success"
	if p.metrics != nil {
		p.metrics.IncWithTags(ctx, "nats_publish_total", 1, tags)
		if !start.IsZero() {
			p.metrics.ObserveWithTags(ctx, "nats_publish_duration_seconds", time.Since(start).Seconds(), tags)
		}
	}
	p.log.DebugCtx(ctx, "message published", zap.String("subject", subject))
	return nil
}
