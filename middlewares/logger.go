package middlewares

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/shadowofcards/go-toolkit/logging"
	"go.uber.org/zap"
)

type Logger struct {
	log *logging.Logger
}

func NewLogger(log *logging.Logger) *Logger {
	return &Logger{log: log}
}

func (l *Logger) Handler() fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		start := time.Now()
		rid := requestid.FromContext(c)

		l.log.InfoCtx(ctx, "request received",
			zap.String("request-id", rid),
			zap.String("method", c.Method()),
			zap.String("path", c.Path()),
			zap.String("query", string(c.Request().URI().QueryString())),
		)

		err := c.Next()

		l.log.InfoCtx(ctx, "response sent",
			zap.String("request-id", rid),
			zap.Int("status", c.Response().StatusCode()),
			zap.Duration("latency_ms", time.Since(start)),
		)

		if err != nil {
			l.log.ErrorCtx(ctx, "request error",
				zap.String("request-id", rid),
				zap.Error(err),
			)
		}
		return err
	}
}
