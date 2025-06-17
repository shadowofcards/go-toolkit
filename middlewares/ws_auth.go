package middlewares

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/shadowofcards/go-toolkit/contexts"
	apperr "github.com/shadowofcards/go-toolkit/errors"
	gtkjwt "github.com/shadowofcards/go-toolkit/jwt"
	"github.com/shadowofcards/go-toolkit/logging"
	"github.com/shadowofcards/go-toolkit/websocket"
	"go.uber.org/zap"
)

var (
	ErrMissingToken      = apperr.New().WithHTTPStatus(http.StatusUnauthorized).WithCode("MISSING_TOKEN").WithMessage("missing token")
	ErrTokenExpiredByAge = apperr.New().WithHTTPStatus(http.StatusUnauthorized).WithCode("TOKEN_EXPIRED").WithMessage("token too old")
	ErrMissingClaim      = apperr.New().WithHTTPStatus(http.StatusUnauthorized).WithCode("MISSING_CLAIM").WithMessage("no subject or player_id in token")
)

type wsJWTClaims struct {
	jwt.RegisteredClaims
	PlayerID          string `json:"player_id"`
	PreferredUsername string `json:"preferred_username"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
	Tid   string   `json:"tid"`
	Perms []string `json:"perms"`
}

type TokenIntrospector interface {
	Introspect(ctx context.Context, token string) (map[string]interface{}, error)
}

type TokenIntrospectorFunc func(ctx context.Context, token string) (map[string]interface{}, error)

func (f TokenIntrospectorFunc) Introspect(ctx context.Context, token string) (map[string]interface{}, error) {
	return f(ctx, token)
}

type WSAuthOption func(*WSAuthMiddleware)

func WithIntrospector(introspector TokenIntrospector) WSAuthOption {
	return func(m *WSAuthMiddleware) {
		m.introspector = introspector
	}
}

type WSAuthMiddleware struct {
	log          *logging.Logger
	verifier     *gtkjwt.Verifier
	introspector TokenIntrospector
	serviceToken string
	appName      string
	env          string
	maxTokenAge  time.Duration
}

func NewWSAuthMiddleware(
	log *logging.Logger,
	verifier *gtkjwt.Verifier,
	serviceToken, appName, env string,
	maxTokenAge time.Duration,
	opts ...WSAuthOption,
) *WSAuthMiddleware {
	m := &WSAuthMiddleware{
		log:          log,
		verifier:     verifier,
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

func (a *WSAuthMiddleware) Middleware() websocket.Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			token := r.URL.Query().Get("token")
			if token == "" {
				writeError(w, ErrMissingToken)
				a.log.WarnCtx(ctx, "missing token query parameter")
				return
			}

			if strings.EqualFold(token, a.serviceToken) {
				ctx = context.WithValue(ctx, contexts.KeyUserID, a.appName)
				ctx = context.WithValue(ctx, contexts.KeyUsername, a.appName)
				ctx = context.WithValue(ctx, contexts.KeyUserRoles, []string{"service"})
				a.log.InfoCtx(ctx, "service token authenticated")
				next(w, r.WithContext(ctx))
				return
			}

			if a.introspector != nil {
				if _, err := a.introspector.Introspect(ctx, token); err != nil {
					a.log.WarnCtx(ctx, "token introspection failed", zap.Error(err))
					writeError(w, ErrInvalidToken)
					return
				}
			}

			var claims wsJWTClaims
			allowExpired := a.env != "production"
			if err := a.verifier.Validate(ctx, token, &claims, allowExpired); err != nil {
				a.log.WarnCtx(ctx, "jwt validation failed", zap.Error(err))
				writeError(w, ErrInvalidToken)
				return
			}

			if a.maxTokenAge > 0 && claims.IssuedAt != nil {
				age := time.Since(claims.IssuedAt.Time)
				if age > a.maxTokenAge {
					a.log.WarnCtx(ctx, "token expired by age")
					writeError(w, ErrTokenExpiredByAge)
					return
				}
			}

			userID := claims.Subject
			if userID == "" {
				userID = claims.PlayerID
			}
			if userID == "" {
				writeError(w, ErrMissingClaim)
				a.log.WarnCtx(ctx, "no subject or player_id in token")
				return
			}

			roles := claims.RealmAccess.Roles
			roles = append(roles, claims.Perms...)

			ctx = context.WithValue(ctx, contexts.KeyTenantID, claims.Tid)
			ctx = context.WithValue(ctx, contexts.KeyUserID, userID)
			ctx = context.WithValue(ctx, contexts.KeyUsername, claims.PreferredUsername)
			ctx = context.WithValue(ctx, contexts.KeyUserRoles, roles)

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
