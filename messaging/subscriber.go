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

// Handler é a função que processa cada mensagem.
type Handler func(ctx context.Context, data []byte) error

// Subscriber consome mensagens de tópicos NATS com concorrência controlada.
type Subscriber struct {
	conn        *nats.Conn
	log         *logging.Logger
	prefix      string
	queue       string
	concurrency int
	deriveCtx   func(context.Context, *nats.Msg) context.Context
}

// SubOption customiza o comportamento do Subscriber.
type SubOption func(*Subscriber)

// Com prefixo para assuntos
func SubWithPrefix(pf string) SubOption { return func(s *Subscriber) { s.prefix = pf } }

// Com nome de queue group
func SubWithQueue(q string) SubOption { return func(s *Subscriber) { s.queue = q } }

// Com grau de concorrência
func SubWithConcurrency(n int) SubOption { return func(s *Subscriber) { s.concurrency = n } }

// Função para derivar o contexto de cada mensagem
func SubWithContextFn(f func(context.Context, *nats.Msg) context.Context) SubOption {
	return func(s *Subscriber) { s.deriveCtx = f }
}

// NewSubscriber cria um Subscriber com opções sensatas.
func NewSubscriber(nc *nats.Conn, log *logging.Logger, opts ...SubOption) *Subscriber {
	s := &Subscriber{
		conn:        nc,
		log:         log,
		concurrency: runtime.NumCPU(),
		deriveCtx: func(parent context.Context, m *nats.Msg) context.Context {
			// injeta RequestID único para rastreamento
			return context.WithValue(parent, contexts.KeyRequestID, xid.New().String())
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Consume inicia a assinatura do subject, processando cada mensagem via h.
// Ele só retorna quando o contexto parent for cancelado.
func (s *Subscriber) Consume(parent context.Context, subject string, h Handler) error {
	if s.prefix != "" {
		subject = s.prefix + subject
	}

	// log de inicialização
	s.log.InfoCtx(parent, "starting subscription",
		zap.String("subject", subject),
		zap.String("queue", s.queue),
		zap.Int("concurrency", s.concurrency),
	)

	// canal com buffer para desacoplar NATS do processamento
	msgCh := make(chan *nats.Msg, s.concurrency*4)
	var wg sync.WaitGroup

	// workers para processar mensagens do canal
	for i := 0; i < s.concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for m := range msgCh {
				ctx := s.deriveCtx(parent, m)
				if err := h(ctx, m.Data); err != nil {
					s.log.ErrorCtx(ctx, "handler error",
						zap.String("subject", subject),
						zap.Error(err),
					)
				} else {
					s.log.DebugCtx(ctx, "message processed",
						zap.String("subject", subject),
					)
				}
			}
		}(i)
	}

	// callback da assinatura NATS
	cb := func(m *nats.Msg) {
		ctx := s.deriveCtx(parent, m)
		select {
		case msgCh <- m:
			s.log.DebugCtx(ctx, "message queued",
				zap.String("subject", subject),
				zap.Int("queue_length", len(msgCh)),
			)
		case <-parent.Done():
			s.log.InfoCtx(ctx, "stopping enqueue: parent context done",
				zap.String("subject", subject),
			)
		default:
			s.log.WarnCtx(ctx, "message dropped: queue full",
				zap.String("subject", subject),
			)
		}
	}

	// realiza a assinatura
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

	// garante que a assinatura foi registrada no servidor
	if err = s.conn.Flush(); err != nil {
		_ = sub.Unsubscribe()
		close(msgCh)
		wg.Wait()
		return err
	}

	s.log.InfoCtx(parent, "subscription ready",
		zap.String("subject", subject),
		zap.String("queue", s.queue),
	)

	// aguarda cancelamento
	<-parent.Done()

	// começa o draining e shutdown
	s.log.InfoCtx(parent, "draining subscription", zap.String("subject", subject))
	_ = sub.Drain()
	close(msgCh)
	wg.Wait()

	s.log.InfoCtx(parent, "subscription stopped",
		zap.String("subject", subject),
		zap.String("queue", s.queue),
	)
	return nil
}
