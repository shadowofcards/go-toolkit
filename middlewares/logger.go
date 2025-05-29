package middlewares

import (
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/shadowofcards/go-toolkit/logging"
	"go.uber.org/zap"
)

type LoggerOption func(*Logger)

// WithLogHeaders enables logging of the specified headers.
// If no keys are passed, logs all headers except "Authorization".
func WithLogHeaders(keys ...string) LoggerOption {
	return func(l *Logger) {
		l.logHeaders = true
		l.headerWhitelist = keys
	}
}

// WithLogAllHeaders is shorthand for logging all headers.
func WithLogAllHeaders() LoggerOption {
	return WithLogHeaders()
}

// WithLogQuery enables or disables query logging.
// By default query logging is enabled with "token" excluded.
func WithLogQuery(enabled bool) LoggerOption {
	return func(l *Logger) {
		l.logQuery = enabled
	}
}

// WithQueryExclusions sets which query parameters to exclude from logs.
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
		start := time.Now()
		rid := requestid.FromContext(c)

		fields := []zap.Field{
			zap.String("request-id", rid),
			zap.String("method", c.Method()),
			zap.String("path", c.Path()),
		}

		// Query logging
		if l.logQuery {
			rawQS := string(c.Request().URI().QueryString())
			safeQS := sanitizeQuery(rawQS, l.queryBlacklist)
			fields = append(fields, zap.String("query", safeQS))
		}

		// Header logging
		if l.logHeaders {
			hdrs := collectHeaders(c, l.headerWhitelist)
			fields = append(fields, zap.Any("headers", hdrs))
		}

		l.log.InfoCtx(ctx, "request received", fields...)

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

// sanitizeQuery removes blacklist keys from the raw query string.
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

// collectHeaders returns a map of headers to log.
// If whitelist is empty, logs all except "Authorization".
func collectHeaders(c fiber.Ctx, whitelist []string) map[string]string {
	out := make(map[string]string)
	reqHdr := &c.Request().Header
	if len(whitelist) > 0 {
		for _, key := range whitelist {
			if vals := reqHdr.Peek(key); len(vals) > 0 {
				out[key] = string(vals)
			}
		}
		return out
	}
	// no whitelist: log all except Authorization
	c.Request().Header.VisitAll(func(k, v []byte) {
		key := string(k)
		if strings.EqualFold(key, "Authorization") {
			return
		}
		out[key] = string(v)
	})
	return out
}
