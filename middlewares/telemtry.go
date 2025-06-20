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

		tags := map[string]string{
			"path":   normalizedPath,
			"method": method,
			"caller": caller,
		}

		_ = rec.GaugeWithTags(ctx, "http_in_flight_requests", 1, tags)

		err := c.Next()

		duration := time.Since(start).Seconds()
		status := c.Response().StatusCode()
		tags["status"] = statusCodeKey(status)

		_ = rec.GaugeWithTags(ctx, "http_in_flight_requests", -1, tags)
		_ = rec.IncWithTags(ctx, "http_requests_total", 1, tags)
		_ = rec.ObserveWithTags(ctx, "http_request_duration_seconds", duration, tags)

		return err
	}
}

func statusCodeKey(code int) string {
	if code < 100 {
		return "UNKNOWN"
	}
	return strings.ReplaceAll(strings.ToUpper(http.StatusText(code)), " ", "_")
}
