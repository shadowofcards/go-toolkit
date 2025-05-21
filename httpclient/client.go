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
	}

	Option func(*BaseClient)
)

/* -------------------------------------------------------------------------- */
/*                                 Options                                    */
/* -------------------------------------------------------------------------- */

func WithHTTPClient(h *http.Client) Option { return func(c *BaseClient) { c.httpClient = h } }
func WithTimeout(t time.Duration) Option   { return func(c *BaseClient) { c.httpClient.Timeout = t } }
func WithAuthToken(tk string) Option       { return func(c *BaseClient) { c.authToken = tk } }
func WithAppName(n string) Option          { return func(c *BaseClient) { c.appName = n } }
func WithLogger(l *logging.Logger) Option  { return func(c *BaseClient) { c.log = l } }

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
	fullURL := c.baseURL + path

	// cancelled before issuing request
	select {
	case <-ctx.Done():
		return errors.New().
			WithError(ctx.Err()).
			WithCode("CTX_CANCELLED").
			WithMessage("request canceled before start").
			WithContext("url", fullURL)
	default:
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

	/* ------------------------------ headers -------------------------------- */

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

	// trace headers
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

	/* ------------------------------- call ---------------------------------- */

	res, err := c.httpClient.Do(req)
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

	/* ------------------------------ error 4xx/5xx -------------------------- */

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

	/* ----------------------------- decode body ----------------------------- */

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
