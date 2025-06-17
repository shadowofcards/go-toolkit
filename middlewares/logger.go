package middlewares

import (
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"go.uber.org/zap"

	"github.com/shadowofcards/go-toolkit/contexts"
	"github.com/shadowofcards/go-toolkit/logging"
)

type LoggerOption func(*Logger)

func WithLogHeaders(keys ...string) LoggerOption {
	return func(l *Logger) {
		l.logHeaders = true
		l.headerWhitelist = keys
	}
}

func WithLogAllHeaders() LoggerOption {
	return WithLogHeaders()
}

func WithLogQuery(enabled bool) LoggerOption {
	return func(l *Logger) {
		l.logQuery = enabled
	}
}

func WithQueryExclusions(excludeKeys ...string) LoggerOption {
	return func(l *Logger) {
		l.queryBlacklist = excludeKeys
	}
}

type Logger struct {
	log             *logging.Logger
	logHeaders      bool
	headerWhitelist []string
	logQuery        bool
	queryBlacklist  []string
}

func NewLogger(log *logging.Logger, opts ...LoggerOption) *Logger {
	l := &Logger{
		log:            log,
		logQuery:       true,
		queryBlacklist: []string{"token"},
	}
	for _, o := range opts {
		o(l)
	}
	return l
}

func (l *Logger) Handler() fiber.Handler {
	return func(c fiber.Ctx) error {

		ctx := c.Context()
		rid := requestid.FromContext(ctx)
		start := time.Now()

		fields := []zap.Field{
			zap.String("request_id", rid),
			zap.String("method", c.Method()),
			zap.String("path", c.Path()),
		}

		if tid, ok := ctx.Value(contexts.KeyTenantID).(string); ok && tid != "" {
			fields = append(fields, zap.String("tenant_id", tid))
		}
		if uid, ok := ctx.Value(contexts.KeyUserID).(string); ok && uid != "" {
			fields = append(fields, zap.String("user_id", uid))
		}

		if l.logQuery {
			rawQS := string(c.Request().URI().QueryString())
			safeQS := sanitizeQuery(rawQS, l.queryBlacklist)
			fields = append(fields, zap.String("query", safeQS))
		}

		if l.logHeaders {
			hdrs := collectHeaders(c, l.headerWhitelist)
			fields = append(fields, zap.Any("headers", hdrs))
		}

		l.log.InfoCtx(ctx, "request received", fields...)

		err := c.Next()

		l.log.InfoCtx(ctx, "response sent",
			zap.String("request_id", rid),
			zap.Int("status", c.Response().StatusCode()),
			zap.Duration("latency", time.Since(start)),
		)

		if err != nil {
			l.log.ErrorCtx(ctx, "request error",
				zap.String("request_id", rid),
				zap.Error(err),
			)
		}
		return err
	}
}

func sanitizeQuery(raw string, blacklist []string) string {
	if raw == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return ""
	}
	for _, k := range blacklist {
		values.Del(k)
	}
	return values.Encode()
}

func collectHeaders(c fiber.Ctx, whitelist []string) map[string]string {
	out := make(map[string]string)
	hdr := &c.Request().Header
	if len(whitelist) > 0 {
		for _, key := range whitelist {
			if v := hdr.Peek(key); len(v) > 0 {
				out[key] = string(v)
			}
		}
		return out
	}
	hdr.VisitAll(func(k, v []byte) {
		key := string(k)
		if strings.EqualFold(key, "authorization") {
			return
		}
		out[key] = string(v)
	})
	return out
}
