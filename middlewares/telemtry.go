package middlewares

import (
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/shadowofcards/go-toolkit/metrics"
)

var (
	uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	numericID   = regexp.MustCompile(`\b\d{4,}\b`)
)

func normalizePath(path, base string) string {
	path = strings.TrimPrefix(path, base)
	path = uuidPattern.ReplaceAllString(path, "<uuid>")
	path = numericID.ReplaceAllString(path, "<id>")
	if path == "" {
		return "/"
	}
	return path
}

func WithHTTPMetrics(rec metrics.Recorder, basePath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		ctx := c.Context()

		_ = rec.Gauge(ctx, "http_in_flight_requests", 1)
		_ = rec.Gauge(ctx, "http_request_size_bytes", float64(len(c.Body())))

		err := c.Next()

		duration := time.Since(start).Seconds()
		status := c.Response().StatusCode()

		path := c.Route().Path
		if path == "" {
			path = normalizePath(c.Path(), basePath)
		}

		_ = rec.Gauge(ctx, "http_in_flight_requests", -1)
		_ = rec.Inc(ctx, "http_requests_total", 1)
		_ = rec.Gauge(ctx, "http_request_duration_seconds", duration)
		_ = rec.Gauge(ctx, "http_response_size_bytes", float64(len(c.Response().Body())))
		_ = rec.Inc(ctx, "http_requests_by_path_"+path, 1)

		statusClass := "<unknown>"
		switch {
		case status >= 100 && status < 200:
			statusClass = "1xx"
		case status >= 200 && status < 300:
			statusClass = "2xx"
		case status >= 300 && status < 400:
			statusClass = "3xx"
		case status >= 400 && status < 500:
			statusClass = "4xx"
			_ = rec.Inc(ctx, "http_client_errors_total", 1)
		case status >= 500:
			statusClass = "5xx"
			_ = rec.Inc(ctx, "http_server_errors_total", 1)
		}

		_ = rec.Inc(ctx, "http_response_status_class_"+statusClass, 1)

		return err
	}
}
