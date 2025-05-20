package middlewares

import (
	"context"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	gjwt "github.com/golang-jwt/jwt/v4"
	"go.uber.org/zap"

	"github.com/leandrodaf/go-toolkit/contexts"
	"github.com/leandrodaf/go-toolkit/errors"
	"github.com/leandrodaf/go-toolkit/logging"
)

type tokenValidator interface {
	Validate(token string, claims gjwt.Claims, allowExpired bool) error
}

type jwtClaims struct {
	gjwt.RegisteredClaims
	PlayerID          string `json:"player_id"`
	PreferredUsername string `json:"preferred_username"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

type AuthMiddleware struct {
	log          *logging.Logger
	validator    tokenValidator
	serviceToken string
	appName      string
	env          string
}

type Option func(*AuthMiddleware)

func WithLogger(l *logging.Logger) Option   { return func(a *AuthMiddleware) { a.log = l } }
func WithValidator(v tokenValidator) Option { return func(a *AuthMiddleware) { a.validator = v } }
func WithServiceToken(t string) Option      { return func(a *AuthMiddleware) { a.serviceToken = t } }
func WithAppName(n string) Option           { return func(a *AuthMiddleware) { a.appName = n } }
func WithEnv(e string) Option               { return func(a *AuthMiddleware) { a.env = e } }

func NewAuthMiddleware(opts ...Option) *AuthMiddleware {
	a := &AuthMiddleware{}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *AuthMiddleware) Handler() fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := a.injectTrace(c)
		if token := c.Get("X-Service-Token"); token != "" {
			return a.authenticateService(ctx, c, token)
		}
		return a.authenticateJWT(ctx, c)
	}
}

func (a *AuthMiddleware) injectTrace(c fiber.Ctx) context.Context {
	ctx := c.Context()
	rid := c.Get("X-Request-Id")
	if rid == "" {
		rid = requestid.FromContext(c)
	}
	ctx = context.WithValue(ctx, contexts.KeyRequestID, rid)
	if v := c.Get("X-App-Name"); v != "" {
		ctx = context.WithValue(ctx, contexts.KeyOrigin, v)
	}
	if v := c.Get("X-User-Agent"); v != "" {
		ctx = context.WithValue(ctx, contexts.KeyUserAgent, v)
	}
	if v := c.Get("X-User-Id"); v != "" {
		ctx = context.WithValue(ctx, contexts.KeyUserID, v)
	}
	if v := c.Get("X-Username"); v != "" {
		ctx = context.WithValue(ctx, contexts.KeyUsername, v)
	}
	if v := c.Get("X-User-Roles"); v != "" {
		ctx = context.WithValue(ctx, contexts.KeyUserRoles, strings.Split(v, ","))
	}
	if v := c.Get("X-Player-Id"); v != "" {
		ctx = context.WithValue(ctx, contexts.KeyPlayerID, v)
	}
	c.SetContext(ctx)
	return ctx
}

func (a *AuthMiddleware) authenticateService(ctx context.Context, c fiber.Ctx, token string) error {
	if token != a.serviceToken {
		return errors.New().WithHTTPStatus(http.StatusUnauthorized).WithCode("INVALID_SERVICE_TOKEN").WithMessage("invalid service token")
	}
	ctx = context.WithValue(ctx, contexts.KeyUserID, a.appName)
	ctx = context.WithValue(ctx, contexts.KeyUsername, a.appName)
	ctx = context.WithValue(ctx, contexts.KeyUserRoles, []string{"service"})
	c.SetContext(ctx)
	a.log.InfoCtx(ctx, "service token authenticated", zap.String("service", a.appName))
	return c.Next()
}

func (a *AuthMiddleware) authenticateJWT(ctx context.Context, c fiber.Ctx) error {
	header := c.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return errors.New().WithHTTPStatus(http.StatusUnauthorized).WithCode("MISSING_OR_MALFORMED_TOKEN").WithMessage("missing or malformed token")
	}
	token := strings.TrimPrefix(header, "Bearer ")
	var claims jwtClaims
	if err := a.validator.Validate(token, &claims, a.env == "production"); err != nil {
		a.log.ErrorCtx(ctx, "jwt validation failed", zap.Error(err))
		return errors.New().WithHTTPStatus(http.StatusUnauthorized).WithCode("INVALID_TOKEN").WithMessage("invalid or expired token")
	}
	userID := claims.Subject
	if userID == "" {
		userID = claims.PlayerID
	}
	ctx = context.WithValue(ctx, contexts.KeyUserID, userID)
	ctx = context.WithValue(ctx, contexts.KeyUsername, claims.PreferredUsername)
	ctx = context.WithValue(ctx, contexts.KeyUserRoles, claims.RealmAccess.Roles)
	ctx = context.WithValue(ctx, contexts.KeyPlayerID, claims.PlayerID)
	c.SetContext(ctx)
	c.Locals("userID", userID)
	c.Locals("username", claims.PreferredUsername)
	c.Locals("roles", claims.RealmAccess.Roles)
	c.Locals("playerID", claims.PlayerID)
	a.log.InfoCtx(ctx, "jwt authenticated", zap.String("user", userID), zap.Strings("roles", claims.RealmAccess.Roles))
	return c.Next()
}
