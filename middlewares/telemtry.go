package middlewares

import (
	"net/http"
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

		routePath := c.Route().Path
		if routePath == "" || routePath == "/" {
			routePath = c.Path()
		}

		normalizedPath := normalizePath(routePath)
		caller := c.Get("X-Caller-ID", "external")

		baseTags := map[string]string{
			"path":   normalizedPath,
			"method": method,
			"caller": caller,
		}

		_ = rec.GaugeWithTags(ctx, "http_in_flight_requests", 1, baseTags)
		_ = rec.GaugeWithTags(ctx, "http_request_size_bytes", float64(len(c.Body())), baseTags)

		err := c.Next()

		duration := time.Since(start).Seconds()
		status := c.Response().StatusCode()
		statusStr := statusCodeKey(status)

		tags := cloneTags(baseTags)
		tags["status"] = statusStr

		_ = rec.GaugeWithTags(ctx, "http_in_flight_requests", -1, tags)
		_ = rec.IncWithTags(ctx, "http_requests_total", 1, tags)
		_ = rec.GaugeWithTags(ctx, "http_request_duration_seconds", duration, tags)
		_ = rec.GaugeWithTags(ctx, "http_response_size_bytes", float64(len(c.Response().Body())), tags)
		_ = rec.GaugeWithTags(ctx, "http_latency_by_path", duration, tags)

		statusClass := "unknown"
		switch {
		case status >= 100 && status < 200:
			statusClass = "1xx"
		case status >= 200 && status < 300:
			statusClass = "2xx"
			_ = rec.IncWithTags(ctx, "http_success_total", 1, tags)
		case status >= 300 && status < 400:
			statusClass = "3xx"
		case status >= 400 && status < 500:
			statusClass = "4xx"
			_ = rec.IncWithTags(ctx, "http_client_errors_total", 1, tags)
			_ = rec.IncWithTags(ctx, "http_errors_total", 1, tags)
		case status >= 500:
			statusClass = "5xx"
			_ = rec.IncWithTags(ctx, "http_server_errors_total", 1, tags)
			_ = rec.IncWithTags(ctx, "http_errors_total", 1, tags)
		}

		tags["status_class"] = statusClass
		_ = rec.IncWithTags(ctx, "http_response_status_class_total", 1, tags)

		return err
	}
}

func statusCodeKey(code int) string {
	if code < 100 {
		return "UNKNOWN"
	}
	return strings.ReplaceAll(strings.ToUpper(http.StatusText(code)), " ", "_")
}

func cloneTags(original map[string]string) map[string]string {
	cloned := make(map[string]string, len(original))
	for k, v := range original {
		cloned[k] = v
	}
	return cloned
}
