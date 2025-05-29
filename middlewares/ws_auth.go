package middlewares

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/shadowofcards/go-toolkit/contexts"
	apperr "github.com/shadowofcards/go-toolkit/errors"
	"github.com/shadowofcards/go-toolkit/logging"
	"github.com/shadowofcards/go-toolkit/websocket"
	"go.uber.org/zap"
)

// TokenIntrospector defines the interface para introspecção de tokens.
type TokenIntrospector interface {
	Introspect(ctx context.Context, token string) (map[string]interface{}, error)
}

// TokenIntrospectorFunc adapta uma função para TokenIntrospector.
type TokenIntrospectorFunc func(ctx context.Context, token string) (map[string]interface{}, error)

func (f TokenIntrospectorFunc) Introspect(ctx context.Context, token string) (map[string]interface{}, error) {
	return f(ctx, token)
}

// WSAuthOption customiza o middleware.
type WSAuthOption func(*WSAuthMiddleware)

// WithIntrospector adiciona suporte a introspecção remota de token.
func WithIntrospector(introspector TokenIntrospector) WSAuthOption {
	return func(m *WSAuthMiddleware) {
		m.introspector = introspector
	}
}

// WSAuthMiddleware autentica handshakes WebSocket via ?token=,
// usando JWT local ou introspecção via TokenIntrospector.
type WSAuthMiddleware struct {
	log          *logging.Logger
	validator    tokenValidator
	introspector TokenIntrospector
	serviceToken string
	appName      string
	env          string
	maxTokenAge  time.Duration
}

// NewWSAuthMiddleware cria o middleware. maxTokenAge=0 desabilita checagem de idade.
// Passe WithIntrospector(...) para habilitar introspecção remota.
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

// Middleware adapta ao tipo websocket.Middleware do toolkit.
func (a *WSAuthMiddleware) Middleware() websocket.Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
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

			if token == a.serviceToken {
				ctx = context.WithValue(ctx, contexts.KeyUserID, a.appName)
				ctx = context.WithValue(ctx, contexts.KeyUsername, a.appName)
				ctx = context.WithValue(ctx, contexts.KeyUserRoles, []string{"service"})
				a.log.InfoCtx(ctx, "service token authenticated")
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
					a.log.ErrorCtx(ctx, "parse introspection failed", zap.Error(err))
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

			if a.maxTokenAge > 0 && time.Now().Unix()-claims.Exp > int64(a.maxTokenAge.Seconds()) {
				a.log.WarnCtx(ctx, "token expired by age")
				writeError(w, apperr.New().
					WithHTTPStatus(http.StatusUnauthorized).
					WithCode("TOKEN_EXPIRED").
					WithMessage("token too old"),
				)
				return
			}

			if a.env != "production" {
				a.log.DebugCtx(ctx, "authenticated token", zap.Any("claims", claims))
			} else {
				a.log.InfoCtx(ctx, "token authenticated", zap.String("user", claims.PreferredUsername))
			}

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
