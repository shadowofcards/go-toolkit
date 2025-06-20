package messaging

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/xid"
	"github.com/shadowofcards/go-toolkit/contexts"
	"github.com/shadowofcards/go-toolkit/logging"
	"github.com/shadowofcards/go-toolkit/metrics"
	"go.uber.org/zap"
)

type ctxKeyNatsMsgID struct{}

type Handler func(ctx context.Context, data []byte) error

type Subscriber struct {
	conn         *nats.Conn
	js           nats.JetStreamContext
	log          *logging.Logger
	prefix       string
	queue        string
	concurrency  int
	deriveCtx    func(context.Context, *nats.Msg) context.Context
	metrics      metrics.Recorder
	useJetStream bool
}

type SubOption func(*Subscriber)

func SubWithPrefix(pf string) SubOption  { return func(s *Subscriber) { s.prefix = pf } }
func SubWithQueue(q string) SubOption    { return func(s *Subscriber) { s.queue = q } }
func SubWithConcurrency(n int) SubOption { return func(s *Subscriber) { s.concurrency = n } }
func SubWithContextFn(f func(context.Context, *nats.Msg) context.Context) SubOption {
	return func(s *Subscriber) { s.deriveCtx = f }
}
func SubWithMetrics(m metrics.Recorder) SubOption { return func(s *Subscriber) { s.metrics = m } }
func SubWithJetStream(enabled bool) SubOption {
	return func(s *Subscriber) { s.useJetStream = enabled }
}

func NewSubscriber(nc *nats.Conn, log *logging.Logger, opts ...SubOption) *Subscriber {
	var js nats.JetStreamContext
	if jsCtx, err := nc.JetStream(); err == nil {
		js = jsCtx
	}
	s := &Subscriber{
		conn:        nc,
		js:          js,
		log:         log,
		concurrency: runtime.NumCPU(),
		deriveCtx: func(parent context.Context, m *nats.Msg) context.Context {
			return context.WithValue(parent, contexts.KeyRequestID, xid.New().String())
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Subscriber) EnsureStream(subject string) error {
	if s.js == nil {
		return nil
	}
	_, err := s.js.StreamInfo(subject)
	if err == nats.ErrStreamNotFound {
		_, err = s.js.AddStream(&nats.StreamConfig{
			Name:     subject,
			Subjects: []string{subject},
		})
	}
	return err
}

func (s *Subscriber) Consume(parent context.Context, subject string, h Handler) error {
	if s.prefix != "" {
		subject = s.prefix + subject
	}
	if s.useJetStream && s.js != nil {
		return s.consumeJetStream(parent, subject, h)
	}
	return s.consumeCore(parent, subject, h)
}

func (s *Subscriber) consumeCore(parent context.Context, subject string, h Handler) error {
	s.log.InfoCtx(parent, "starting NATS subscription",
		zap.String("subject", subject),
		zap.String("queue", s.queue),
		zap.Int("concurrency", s.concurrency),
	)
	msgCh := make(chan *nats.Msg, s.concurrency*4)
	var wg sync.WaitGroup
	for i := 0; i < s.concurrency; i++ {
		workerID := i
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for m := range msgCh {
				ctx := s.deriveCtx(parent, m)
				start := time.Now()
				tags := map[string]string{
					"subject": subject,
					"queue":   s.queue,
					"worker":  workerTag(workerID),
				}
				if s.metrics != nil {
					s.metrics.IncWithTags(ctx, "nats_consume_total", 1, mergeTags(tags, map[string]string{"status": "received"}))
				}
				if err := h(ctx, m.Data); err != nil {
					if s.metrics != nil {
						s.metrics.IncWithTags(ctx, "nats_consume_total", 1, mergeTags(tags, map[string]string{"status": "error"}))
						s.metrics.ObserveWithTags(ctx, "nats_consume_duration_seconds", time.Since(start).Seconds(), tags)
					}
					s.log.ErrorCtx(ctx, "handler error", zap.String("subject", subject), zap.Error(err))
				} else {
					if s.metrics != nil {
						s.metrics.IncWithTags(ctx, "nats_consume_total", 1, mergeTags(tags, map[string]string{"status": "processed"}))
						s.metrics.ObserveWithTags(ctx, "nats_consume_duration_seconds", time.Since(start).Seconds(), tags)
					}
					s.log.DebugCtx(ctx, "message processed", zap.String("subject", subject))
				}
			}
		}(workerID)
	}
	cb := func(m *nats.Msg) {
		ctx := s.deriveCtx(parent, m)
		select {
		case msgCh <- m:
			s.log.DebugCtx(ctx, "message queued", zap.String("subject", subject), zap.Int("queue_length", len(msgCh)))
		case <-parent.Done():
			s.log.InfoCtx(ctx, "stopping enqueue: parent context done", zap.String("subject", subject))
		default:
			if s.metrics != nil {
				s.metrics.IncWithTags(ctx, "nats_consume_total", 1, map[string]string{
					"subject": subject,
					"queue":   s.queue,
					"status":  "dropped",
				})
			}
			s.log.WarnCtx(ctx, "message dropped: queue full", zap.String("subject", subject))
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
	s.log.InfoCtx(parent, "subscription ready", zap.String("subject", subject), zap.String("queue", s.queue))
	<-parent.Done()
	s.log.InfoCtx(parent, "draining subscription", zap.String("subject", subject))
	_ = sub.Drain()
	close(msgCh)
	wg.Wait()
	s.log.InfoCtx(parent, "subscription stopped", zap.String("subject", subject), zap.String("queue", s.queue))
	return nil
}

func (s *Subscriber) consumeJetStream(parent context.Context, subject string, h Handler) error {
	if err := s.EnsureStream(subject); err != nil {
		return err
	}
	consumerName := s.queue
	if consumerName == "" {
		consumerName = "default"
	}
	sub, err := s.js.PullSubscribe(subject, consumerName, nats.BindStream(subject))
	if err != nil {
		return err
	}
	s.log.InfoCtx(parent, "JetStream subscription ready", zap.String("subject", subject), zap.String("queue", consumerName))
	for {
		select {
		case <-parent.Done():
			return nil
		default:
			msgs, err := sub.Fetch(10, nats.MaxWait(2*time.Second))
			if err != nil && err != nats.ErrTimeout {
				s.log.ErrorCtx(parent, "JetStream fetch error", zap.Error(err))
				continue
			}
			for _, msg := range msgs {
				msgID := msg.Header.Get("Nats-Msg-Id")
				ctx := context.WithValue(parent, ctxKeyNatsMsgID{}, msgID)
				start := time.Now()
				tags := map[string]string{
					"subject": subject,
					"queue":   consumerName,
				}
				if s.metrics != nil {
					s.metrics.IncWithTags(ctx, "nats_consume_total", 1, mergeTags(tags, map[string]string{"status": "received"}))
				}
				err := h(ctx, msg.Data)
				if err != nil {
					if s.metrics != nil {
						s.metrics.IncWithTags(ctx, "nats_consume_total", 1, mergeTags(tags, map[string]string{"status": "error"}))
						s.metrics.ObserveWithTags(ctx, "nats_consume_duration_seconds", time.Since(start).Seconds(), tags)
					}
					s.log.ErrorCtx(ctx, "handler error", zap.String("subject", subject), zap.Error(err))
					msg.Nak()
					continue
				}
				if s.metrics != nil {
					s.metrics.IncWithTags(ctx, "nats_consume_total", 1, mergeTags(tags, map[string]string{"status": "processed"}))
					s.metrics.ObserveWithTags(ctx, "nats_consume_duration_seconds", time.Since(start).Seconds(), tags)
				}
				msg.Ack()
			}
		}
	}
}

func mergeTags(a, b map[string]string) map[string]string {
	tags := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		tags[k] = v
	}
	for k, v := range b {
		tags[k] = v
	}
	return tags
}

func workerTag(id int) string {
	return "worker" + strconv.Itoa(id)
}
