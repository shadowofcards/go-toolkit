package logging

import (
	"context"
	"strings"

	"github.com/leandrodaf/go-toolkit/contexts"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger struct{ *zap.Logger }

type Option func(*zap.Config)

func New(opts ...Option) (*Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	for _, opt := range opts {
		opt(&cfg)
	}

	zl, err := cfg.Build(zap.AddCaller(), zap.AddCallerSkip(1))
	if err != nil {
		return nil, err
	}
	return &Logger{zl}, nil
}

func WithLevel(level string) Option {
	return func(cfg *zap.Config) {
		cfg.Level = zap.NewAtomicLevelAt(toLevel(level))
	}
}

func WithDevelopmentEncoder() Option {
	return func(cfg *zap.Config) {
		dev := zap.NewDevelopmentConfig()
		dev.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		cfg.Encoding = dev.Encoding
		cfg.EncoderConfig = dev.EncoderConfig
	}
}

func toLevel(lvl string) zapcore.Level {
	switch strings.ToLower(lvl) {
	case "debug":
		return zapcore.DebugLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func (l *Logger) with(ctx context.Context) *Logger {
	if v, ok := ctx.Value(contexts.KeyRequestID).(string); ok && v != "" {
		return &Logger{l.Logger.With(zap.String("request-id", v))}
	}
	return l
}

func (l *Logger) InfoCtx(ctx context.Context, msg string, f ...zap.Field) {
	l.with(ctx).Info(msg, f...)
}
func (l *Logger) DebugCtx(ctx context.Context, msg string, f ...zap.Field) {
	l.with(ctx).Debug(msg, f...)
}
func (l *Logger) WarnCtx(ctx context.Context, msg string, f ...zap.Field) {
	l.with(ctx).Warn(msg, f...)
}
func (l *Logger) ErrorCtx(ctx context.Context, msg string, f ...zap.Field) {
	l.with(ctx).Error(msg, f...)
}
