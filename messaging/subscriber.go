package messaging

import (
	"context"
	"runtime"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/rs/xid"
	"github.com/shadowofcards/go-toolkit/contexts"
	"github.com/shadowofcards/go-toolkit/logging"
	"go.uber.org/zap"
)

type Handler func(ctx context.Context, data []byte) error

type Subscriber struct {
	conn        *nats.Conn
	log         *logging.Logger
	prefix      string
	queue       string
	concurrency int
	deriveCtx   func(context.Context, *nats.Msg) context.Context
}

type SubOption func(*Subscriber)

func SubWithPrefix(pf string) SubOption  { return func(s *Subscriber) { s.prefix = pf } }
func SubWithQueue(q string) SubOption    { return func(s *Subscriber) { s.queue = q } }
func SubWithConcurrency(n int) SubOption { return func(s *Subscriber) { s.concurrency = n } }
func SubWithContextFn(f func(context.Context, *nats.Msg) context.Context) SubOption {
	return func(s *Subscriber) { s.deriveCtx = f }
}

func NewSubscriber(nc *nats.Conn, log *logging.Logger, opts ...SubOption) *Subscriber {
	s := &Subscriber{
		conn:        nc,
		log:         log,
		concurrency: runtime.NumCPU(),
		deriveCtx: func(parent context.Context, _ *nats.Msg) context.Context {
			return context.WithValue(parent, contexts.KeyRequestID, xid.New().String())
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Subscriber) Consume(parent context.Context, subject string, h Handler) error {
	if s.prefix != "" {
		subject = s.prefix + subject
	}
	msgCh := make(chan *nats.Msg, s.concurrency*4)
	var wg sync.WaitGroup
	for i := 0; i < s.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range msgCh {
				ctx, cancel := context.WithCancel(s.deriveCtx(parent, m))
				if err := h(ctx, m.Data); err != nil {
					s.log.ErrorCtx(ctx, "handler error", zap.String("subject", subject), zap.Error(err))
				} else {
					s.log.DebugCtx(ctx, "message processed", zap.String("subject", subject))
				}
				cancel()
			}
		}()
	}
	cb := func(m *nats.Msg) {
		select {
		case msgCh <- m:
			s.log.Debug("message queued", zap.String("subject", subject))
		case <-parent.Done():
		default:
			s.log.Warn("message dropped (queue full)", zap.String("subject", subject))
		}
	}
	var sub *nats.Subscription
	var err error
	if s.queue != "" {
		sub, err = s.conn.QueueSubscribe(subject, s.queue, cb)
	} else {
		sub, err = s.conn.Subscribe(subject, cb)
	}
	if err != nil {
		close(msgCh)
		wg.Wait()
		return err
	}
	if err = s.conn.Flush(); err != nil {
		_ = sub.Unsubscribe()
		close(msgCh)
		wg.Wait()
		return err
	}
	s.log.Info("subscription started", zap.String("subject", subject), zap.String("queue", s.queue))
	<-parent.Done()
	_ = sub.Drain()
	close(msgCh)
	wg.Wait()
	s.log.Info("subscription stopped", zap.String("subject", subject), zap.String("queue", s.queue))
	return nil
}
