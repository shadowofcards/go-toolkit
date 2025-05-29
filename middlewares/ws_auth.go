// internal/system/http/middlewares/ws_auth.go
package middlewares

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/shadowofcards/go-toolkit/contexts"
	"github.com/shadowofcards/go-toolkit/logging"
	"github.com/shadowofcards/go-toolkit/websocket"
	"go.uber.org/zap"
)

type WSAuthMiddleware struct {
	log          *logging.Logger
	validator    tokenValidator
	serviceToken string
	appName      string
	env          string
}

func NewWSAuthMiddleware(
	log *logging.Logger,
	validator tokenValidator,
	serviceToken, appName, env string,
) *WSAuthMiddleware {
	return &WSAuthMiddleware{log, validator, serviceToken, appName, env}
}

func (a *WSAuthMiddleware) Middleware() websocket.Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// start from existing context
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

			// extract token from query
			token := r.URL.Query().Get("token")
			if token == "" {
				writeError(w, http.StatusUnauthorized, "MISSING_TOKEN", "missing token query parameter")
				return
			}

			// service token shortcut
			if token == a.serviceToken {
				ctx = context.WithValue(ctx, contexts.KeyUserID, a.appName)
				ctx = context.WithValue(ctx, contexts.KeyUsername, a.appName)
				ctx = context.WithValue(ctx, contexts.KeyUserRoles, []string{"service"})
				next(w, r.WithContext(ctx))
				return
			}

			// validate JWT
			var claims jwtClaims
			if err := a.validator.Validate(token, &claims, a.env == "production"); err != nil {
				a.log.ErrorCtx(ctx, "jwt validation failed", zap.Error(err))
				writeError(w, http.StatusUnauthorized, "INVALID_TOKEN", "invalid or expired token")
				return
			}

			// debug-log full claims
			a.log.DebugCtx(ctx, "decoded JWT claims", zap.Any("claims", claims))

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

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
