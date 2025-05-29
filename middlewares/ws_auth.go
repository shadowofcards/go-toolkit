// internal/system/http/middlewares/ws_auth.go
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

type TokenIntrospector interface {
	Introspect(ctx context.Context, token string) (map[string]interface{}, error)
}

type WSAuthOption func(*WSAuthMiddleware)

func WithIntrospector(intro TokenIntrospector) WSAuthOption {
	return func(m *WSAuthMiddleware) { m.introspector = intro }
}

type WSAuthMiddleware struct {
	log          *logging.Logger
	validator    tokenValidator
	introspector TokenIntrospector
	serviceToken string
	appName, env string
	maxTokenAge  time.Duration
}

// NewWSAuthMiddleware constructs a WSAuthMiddleware.
// Pass WithIntrospector(...) to enable introspection, and
// pass a maxTokenAge to reject tokens older than that.
func NewWSAuthMiddleware(
	log *logging.Logger,
	validator tokenValidator,
	serviceToken, appName, env string,
	maxTokenAge time.Duration,
	opts ...WSAuthOption,
) *WSAuthMiddleware {
	m := &WSAuthMiddleware{log: log, validator: validator, serviceToken: serviceToken, appName: appName, env: env, maxTokenAge: maxTokenAge}
	for _, o := range opts {
		o(m)
	}
	return m
}

func (a *WSAuthMiddleware) Middleware() websocket.Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			// trace headers
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
				a.log.WarnCtx(ctx, "missing token")
				return
			}

			// constant-time service token check
			if strings.EqualFold(token, a.serviceToken) {
				a.log.InfoCtx(ctx, "service token auth", zap.String("service", a.appName))
				ctx = context.WithValue(ctx, contexts.KeyUserID, a.appName)
				ctx = context.WithValue(ctx, contexts.KeyUsername, a.appName)
				ctx = context.WithValue(ctx, contexts.KeyUserRoles, []string{"service"})
				next(w, r.WithContext(ctx))
				return
			}

			var claims jwtClaims
			if a.introspector != nil {
				raw, err := a.introspector.Introspect(ctx, token)
				if err != nil {
					a.log.WarnCtx(ctx, "introspection failed", zap.Error(err))
					writeError(w, apperr.New().
						WithHTTPStatus(http.StatusUnauthorized).
						WithCode("INVALID_TOKEN").
						WithMessage("invalid token"),
					)
					return
				}
				data, _ := json.Marshal(raw)
				if err := json.Unmarshal(data, &claims); err != nil {
					a.log.ErrorCtx(ctx, "parse introspected claims failed", zap.Error(err))
					writeError(w, apperr.New().
						WithHTTPStatus(http.StatusInternalServerError).
						WithCode("CLAIM_PARSE_ERROR").
						WithMessage("could not parse claims"),
					)
					return
				}
			} else {
				if err := a.validator.Validate(token, &claims, a.env == "production"); err != nil {
					a.log.WarnCtx(ctx, "jwt validation failed", zap.Error(err))
					writeError(w, apperr.New().
						WithHTTPStatus(http.StatusUnauthorized).
						WithCode("INVALID_TOKEN").
						WithMessage("invalid or expired token"),
					)
					return
				}
			}

			// optional token age check
			if a.maxTokenAge > 0 {
				if time.Now().Unix()-claims.Exp > int64(a.maxTokenAge.Seconds()) {
					writeError(w, apperr.New().
						WithHTTPStatus(http.StatusUnauthorized).
						WithCode("TOKEN_EXPIRED").
						WithMessage("token too old"),
					)
					a.log.WarnCtx(ctx, "token expired by age")
					return
				}
			}

			if a.env != "production" {
				a.log.DebugCtx(ctx, "authenticated token", zap.Any("claims", claims))
			} else {
				a.log.InfoCtx(ctx, "token authenticated", zap.String("user", claims.PreferredUsername))
			}

			// inject into context
			userID := claims.Subject
			if userID == "" {
				userID = claims.PlayerID
			}
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
