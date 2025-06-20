package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shadowofcards/go-toolkit/contexts"
	"github.com/shadowofcards/go-toolkit/errors"
	"github.com/shadowofcards/go-toolkit/logging"
	"github.com/shadowofcards/go-toolkit/metrics"
	"go.uber.org/zap"
)

/* -------------------------------------------------------------------------- */
/*                                   Types                                    */
/* -------------------------------------------------------------------------- */

type (
	BaseClient struct {
		baseURL    string
		httpClient *http.Client
		authToken  string
		appName    string
		log        *logging.Logger
		metrics    metrics.Recorder
	}

	Option func(*BaseClient)
)

/* -------------------------------------------------------------------------- */
/*                                 Options                                    */
/* -------------------------------------------------------------------------- */

func WithHTTPClient(h *http.Client) Option {
	return func(c *BaseClient) {
		if h == nil {
			h = &http.Client{}
		}
		c.httpClient = h
	}
}

func WithTimeout(t time.Duration) Option {
	return func(c *BaseClient) {
		if c.httpClient == nil {
			c.httpClient = &http.Client{}
		}
		c.httpClient.Timeout = t
	}
}

func WithAuthToken(tk string) Option        { return func(c *BaseClient) { c.authToken = tk } }
func WithAppName(n string) Option           { return func(c *BaseClient) { c.appName = n } }
func WithLogger(l *logging.Logger) Option   { return func(c *BaseClient) { c.log = l } }
func WithMetrics(m metrics.Recorder) Option { return func(c *BaseClient) { c.metrics = m } }

/* -------------------------------------------------------------------------- */
/*                               Constructor                                  */
/* -------------------------------------------------------------------------- */

func New(baseURL string, opts ...Option) *BaseClient {
	bc := &BaseClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(bc)
	}
	if bc.httpClient == nil {
		bc.httpClient = &http.Client{Timeout: 10 * time.Second}
	} else if bc.httpClient.Timeout == 0 {
		bc.httpClient.Timeout = 10 * time.Second
	}
	return bc
}

/* -------------------------------------------------------------------------- */

type apiErrPayload struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

/* -------------------------------------------------------------------------- */
/*                                   Do                                       */
/* -------------------------------------------------------------------------- */

func (c *BaseClient) Do(ctx context.Context, method, path string, body io.Reader, v any) error {
	if c.httpClient == nil {
		if c.log != nil {
			c.log.ErrorCtx(ctx, "nil httpClient detected")
		}
		return errors.New().
			WithCode("NIL_HTTP_CLIENT").
			WithMessage("httpClient is nil – use httpclient.New or provide one via option")
	}

	fullURL := c.baseURL + path

	select {
	case <-ctx.Done():
		return errors.New().
			WithError(ctx.Err()).
			WithCode("CTX_CANCELLED").
			WithMessage("request canceled before start").
			WithContext("url", fullURL)
	default:
	}

	var start time.Time
	if c.metrics != nil {
		start = time.Now()
	}

	if c.log != nil {
		c.log.InfoCtx(ctx, "HTTP request start",
			zap.String("method", method),
			zap.String("url", fullURL),
		)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		if c.log != nil {
			c.log.ErrorCtx(ctx, "failed to build request", zap.Error(err))
		}
		return errors.New().
			WithError(err).
			WithMessage("failed to build HTTP request").
			WithContext("url", fullURL)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.authToken != "" {
		req.Header.Set("X-Service-Token", c.authToken)
	}
	if c.appName != "" {
		req.Header.Set("X-App-Name", c.appName)
	}

	if rid, ok := ctx.Value(contexts.KeyRequestID).(string); ok {
		req.Header.Set("X-Request-Id", rid)
	}
	if origin, ok := ctx.Value(contexts.KeyOrigin).(string); ok {
		req.Header.Set("X-Origin", origin)
	}
	if ua, ok := ctx.Value(contexts.KeyUserAgent).(string); ok {
		req.Header.Set("X-User-Agent", ua)
	}
	if userID, ok := ctx.Value(contexts.KeyUserID).(string); ok {
		req.Header.Set("X-User-Id", userID)
	}
	if username, ok := ctx.Value(contexts.KeyUsername).(string); ok {
		req.Header.Set("X-Username", username)
	}
	if roles := ctx.Value(contexts.KeyUserRoles); roles != nil {
		switch r := roles.(type) {
		case []string:
			req.Header.Set("X-User-Roles", strings.Join(r, ","))
		case string:
			req.Header.Set("X-User-Roles", r)
		}
	}
	if pid, ok := ctx.Value(contexts.KeyPlayerID).(string); ok {
		req.Header.Set("X-Player-Id", pid)
	}

	res, err := c.httpClient.Do(req)
	if c.metrics != nil {
		duration := float64(0)
		if !start.IsZero() {
			duration = time.Since(start).Seconds()
		}
		tags := map[string]string{
			"method": strings.ToUpper(method),
			"path":   path,
			"host":   req.URL.Host,
		}
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		tags["status"] = statusCodeKey(status)
		c.metrics.IncWithTags(ctx, "http_client_requests_total", 1, tags)
		c.metrics.ObserveWithTags(ctx, "http_client_request_duration_seconds", duration, tags)
	}

	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			code := "CTX_ERROR"
			if ctxErr == context.Canceled {
				code = "CTX_CANCELED"
			} else if ctxErr == context.DeadlineExceeded {
				code = "CTX_DEADLINE"
			}
			return errors.New().
				WithError(ctxErr).
				WithCode(code).
				WithMessage("request canceled or timed out").
				WithContext("url", fullURL)
		}
		if c.log != nil {
			c.log.ErrorCtx(ctx, "HTTP request failed", zap.Error(err))
		}
		return errors.New().
			WithError(err).
			WithMessage("HTTP request failed").
			WithContext("url", fullURL)
	}
	defer res.Body.Close()

	bodyBytes, _ := io.ReadAll(res.Body)

	if res.StatusCode >= 400 {
		var payload apiErrPayload
		code := fmt.Sprintf("HTTP_%d", res.StatusCode)
		msg := "service returned error"
		if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.Error.Code != "" {
			code = payload.Error.Code
			msg = payload.Error.Message
		}
		if c.log != nil {
			c.log.WarnCtx(ctx, "HTTP error response",
				zap.Int("status", res.StatusCode),
				zap.String("error_code", code),
			)
		}
		return errors.New().
			WithHTTPStatus(res.StatusCode).
			WithCode(code).
			WithMessage(msg).
			WithContext("url", fullURL).
			WithContext("status", res.StatusCode).
			WithContext("body", string(bodyBytes))
	}

	if v != nil {
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(v); err != nil {
			if c.log != nil {
				c.log.ErrorCtx(ctx, "failed to decode response", zap.Error(err))
			}
			return errors.New().
				WithError(err).
				WithCode("DECODE_ERROR").
				WithMessage("failed to decode JSON").
				WithContext("url", fullURL)
		}
	}

	if c.log != nil {
		c.log.InfoCtx(ctx, "HTTP request success", zap.Int("status", res.StatusCode))
	}
	return nil
}

func statusCodeKey(code int) string {
	if code < 100 {
		return "UNKNOWN"
	}
	return fmt.Sprintf("%d", code)
}
