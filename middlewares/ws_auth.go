package middlewares

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/shadowofcards/go-toolkit/contexts"
	apperr "github.com/shadowofcards/go-toolkit/errors"
	"github.com/shadowofcards/go-toolkit/logging"
	"github.com/shadowofcards/go-toolkit/websocket"
	"go.uber.org/zap"
)

// TokenIntrospector defines the interface for remote token lookup.
type TokenIntrospector interface {
	Introspect(ctx context.Context, token string) (map[string]interface{}, error)
}

// TokenIntrospectorFunc adapts a function to TokenIntrospector.
type TokenIntrospectorFunc func(ctx context.Context, token string) (map[string]interface{}, error)

func (f TokenIntrospectorFunc) Introspect(ctx context.Context, token string) (map[string]interface{}, error) {
	return f(ctx, token)
}

// WSAuthOption allows customizing WSAuthMiddleware behavior.
type WSAuthOption func(*WSAuthMiddleware)

// WithIntrospector enables remote introspection of tokens.
func WithIntrospector(introspector TokenIntrospector) WSAuthOption {
	return func(m *WSAuthMiddleware) {
		m.introspector = introspector
	}
}

// WSAuthMiddleware handles WebSocket auth via JWT in ?token= or service token.
type WSAuthMiddleware struct {
	log          *logging.Logger
	validator    tokenValidator
	introspector TokenIntrospector
	serviceToken string
	appName      string
	env          string
	maxTokenAge  time.Duration
}

// NewWSAuthMiddleware creates a middleware instance.
// maxTokenAge==0 disables token-age checks.
// Pass WithIntrospector(...) to enable remote introspection.
func NewWSAuthMiddleware(
	log *logging.Logger,
	validator tokenValidator,
	serviceToken, appName, env string,
	maxTokenAge time.Duration,
	opts ...WSAuthOption,
) *WSAuthMiddleware {
	m := &WSAuthMiddleware{
		log:          log,
		validator:    validator,
		serviceToken: serviceToken,
		appName:      appName,
		env:          env,
		maxTokenAge:  maxTokenAge,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Middleware adapts WSAuthMiddleware into the toolkit's websocket.Middleware.
func (a *WSAuthMiddleware) Middleware() websocket.Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// propagate tracing headers
			if rid := r.Header.Get("X-Request-Id"); rid != "" {
				ctx = context.WithValue(ctx, contexts.KeyRequestID, rid)
			}
			if v := r.Header.Get("X-App-Name"); v != "" {
				ctx = context.WithValue(ctx, contexts.KeyOrigin, v)
			}
			if v := r.Header.Get("X-User-Agent"); v != "" {
				ctx = context.WithValue(ctx, contexts.KeyUserAgent, v)
			}

			token := r.URL.Query().Get("token")
			if token == "" {
				writeError(w, apperr.New().
					WithHTTPStatus(http.StatusUnauthorized).
					WithCode("MISSING_TOKEN").
					WithMessage("missing token"),
				)
				a.log.WarnCtx(ctx, "missing token query parameter")
				return
			}

			// service-token shortcut
			if strings.EqualFold(token, a.serviceToken) {
				ctx = context.WithValue(ctx, contexts.KeyUserID, a.appName)
				ctx = context.WithValue(ctx, contexts.KeyUsername, a.appName)
				ctx = context.WithValue(ctx, contexts.KeyUserRoles, []string{"service"})
				a.log.InfoCtx(ctx, "service token authenticated")
				next(w, r.WithContext(ctx))
				return
			}

			// optionally introspect to confirm active
			if a.introspector != nil {
				if _, err := a.introspector.Introspect(ctx, token); err != nil {
					a.log.WarnCtx(ctx, "token introspection failed", zap.Error(err))
					writeError(w, apperr.New().
						WithHTTPStatus(http.StatusUnauthorized).
						WithCode("INVALID_TOKEN").
						WithMessage("invalid token"),
					)
					return
				}
			}

			// parse and validate JWT claims
			var claims jwtClaims
			if err := a.validator.Validate(token, &claims, a.env == "production"); err != nil {
				a.log.WarnCtx(ctx, "jwt validation failed", zap.Error(err))
				writeError(w, apperr.New().
					WithHTTPStatus(http.StatusUnauthorized).
					WithCode("INVALID_TOKEN").
					WithMessage("invalid or expired token"),
				)
				return
			}

			// optional token age check
			if a.maxTokenAge > 0 && time.Now().Unix()-claims.Exp > int64(a.maxTokenAge.Seconds()) {
				a.log.WarnCtx(ctx, "token expired by age")
				writeError(w, apperr.New().
					WithHTTPStatus(http.StatusUnauthorized).
					WithCode("TOKEN_EXPIRED").
					WithMessage("token too old"),
				)
				return
			}

			// log summary
			if a.env != "production" {
				a.log.DebugCtx(ctx, "authenticated token claims", zap.Any("claims", claims))
			} else {
				a.log.InfoCtx(ctx, "token authenticated", zap.String("user", claims.PreferredUsername))
			}

			// extract playerID (fallback to Subject)
			userID := claims.Subject
			if userID == "" {
				userID = claims.PlayerID
			}
			if userID == "" {
				writeError(w, apperr.New().
					WithHTTPStatus(http.StatusUnauthorized).
					WithCode("MISSING_CLAIM").
					WithMessage("no subject or player_id in token"),
				)
				a.log.WarnCtx(ctx, "no playerID or sub in token")
				return
			}

			// inject into context
			ctx = context.WithValue(ctx, contexts.KeyUserID, userID)
			ctx = context.WithValue(ctx, contexts.KeyUsername, claims.PreferredUsername)
			ctx = context.WithValue(ctx, contexts.KeyUserRoles, claims.RealmAccess.Roles)
			ctx = context.WithValue(ctx, contexts.KeyPlayerID, claims.PlayerID)

			next(w, r.WithContext(ctx))
		}
	}
}

func writeError(w http.ResponseWriter, appErr *apperr.AppError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(appErr.Status())
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    appErr.ErrCode(),
		"message": appErr.Message,
	})
}
