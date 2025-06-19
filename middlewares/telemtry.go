package middlewares

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/shadowofcards/go-toolkit/metrics"
)

var (
	uuidPattern  = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	numericID    = regexp.MustCompile(`\b\d{4,}\b`)
	leadingSlash = regexp.MustCompile(`^/+`)
)

func normalizePath(path string) string {
	path = uuidPattern.ReplaceAllString(path, "<uuid>")
	path = numericID.ReplaceAllString(path, "<id>")
	path = leadingSlash.ReplaceAllString(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func WithHTTPMetrics(rec metrics.Recorder) fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		ctx := c.Context()

		method := string(c.Method())

		_ = rec.Gauge(ctx, "http_in_flight_requests", 1)
		_ = rec.Gauge(ctx, "http_request_size_bytes", float64(len(c.Body())))

		err := c.Next()

		duration := time.Since(start).Seconds()
		status := c.Response().StatusCode()

		routePath := c.Route().Path
		if routePath == "" {
			routePath = c.OriginalURL()
		}
		normalizedPath := normalizePath(routePath)

		_ = rec.Gauge(ctx, "http_in_flight_requests", -1)
		_ = rec.Inc(ctx, "http_requests_total", 1)
		_ = rec.Inc(ctx, "http_requests_by_method_"+method, 1)
		_ = rec.Inc(ctx, "http_requests_by_status_"+statusCodeKey(status), 1)
		_ = rec.Inc(ctx, "http_requests_by_path_"+normalizedPath, 1)
		_ = rec.Gauge(ctx, "http_request_duration_seconds", duration)
		_ = rec.Gauge(ctx, "http_latency_by_path_"+normalizedPath, duration)
		_ = rec.Gauge(ctx, "http_response_size_bytes", float64(len(c.Response().Body())))

		statusClass := "<unknown>"
		switch {
		case status >= 100 && status < 200:
			statusClass = "1xx"
		case status >= 200 && status < 300:
			statusClass = "2xx"
			_ = rec.Inc(ctx, "http_success_total", 1)
		case status >= 300 && status < 400:
			statusClass = "3xx"
		case status >= 400 && status < 500:
			statusClass = "4xx"
			_ = rec.Inc(ctx, "http_client_errors_total", 1)
			_ = rec.Inc(ctx, "http_errors_total", 1)
		case status >= 500:
			statusClass = "5xx"
			_ = rec.Inc(ctx, "http_server_errors_total", 1)
			_ = rec.Inc(ctx, "http_errors_total", 1)
		}

		_ = rec.Inc(ctx, "http_response_status_class_"+statusClass, 1)

		return err
	}
}

func statusCodeKey(code int) string {
	return strings.ReplaceAll(fmt.Sprintf("%03d", code), ".", "_")
}
