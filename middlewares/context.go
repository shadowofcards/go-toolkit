package middlewares

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	gjwt "github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/shadowofcards/go-toolkit/contexts"
	apperrors "github.com/shadowofcards/go-toolkit/errors"
	"github.com/shadowofcards/go-toolkit/jwt"
	"github.com/shadowofcards/go-toolkit/logging"
)

var (
	ErrMissingOrMalformedToken = apperrors.New().
					WithHTTPStatus(http.StatusUnauthorized).
					WithCode("MISSING_OR_MALFORMED_TOKEN").
					WithMessage("missing or malformed token")

	ErrTokenMalformed = apperrors.New().
				WithHTTPStatus(http.StatusBadRequest).
				WithCode("TOKEN_MALFORMED").
				WithMessage("token is malformed")

	ErrTokenUnverifiable = apperrors.New().
				WithHTTPStatus(http.StatusBadRequest).
				WithCode("TOKEN_UNVERIFIABLE").
				WithMessage("token could not be verified")

	ErrInvalidSignature = apperrors.New().
				WithHTTPStatus(http.StatusUnauthorized).
				WithCode("INVALID_SIGNATURE").
				WithMessage("token signature is invalid")

	ErrTokenExpired = apperrors.New().
			WithHTTPStatus(http.StatusUnauthorized).
			WithCode("TOKEN_EXPIRED").
			WithMessage("token is expired")

	ErrInvalidToken = apperrors.New().
			WithHTTPStatus(http.StatusUnauthorized).
			WithCode("INVALID_TOKEN").
			WithMessage("invalid token")

	ErrInvalidServiceToken = apperrors.New().
				WithHTTPStatus(http.StatusUnauthorized).
				WithCode("INVALID_SERVICE_TOKEN").
				WithMessage("invalid service token")
)

type AuthMiddleware struct {
	log          *logging.Logger
	verifier     *jwt.Verifier
	serviceToken string
	appName      string
	env          string
}

type Option func(*AuthMiddleware)

func WithLogger(l *logging.Logger) Option { return func(a *AuthMiddleware) { a.log = l } }
func WithVerifier(v *jwt.Verifier) Option { return func(a *AuthMiddleware) { a.verifier = v } }
func WithServiceToken(t string) Option    { return func(a *AuthMiddleware) { a.serviceToken = t } }
func WithAppName(n string) Option         { return func(a *AuthMiddleware) { a.appName = n } }
func WithEnv(e string) Option             { return func(a *AuthMiddleware) { a.env = e } }

func NewAuthMiddleware(opts ...Option) *AuthMiddleware {
	am := &AuthMiddleware{}
	for _, o := range opts {
		o(am)
	}
	return am
}

func (a *AuthMiddleware) Handler() fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := injectTrace(c)
		if token := c.Get("X-Service-Token"); token != "" {
			return a.authenticateService(ctx, c, token)
		}
		return a.authenticateJWT(ctx, c)
	}
}

func injectTrace(c fiber.Ctx) context.Context {
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
	c.SetContext(ctx)
	return ctx
}

func (a *AuthMiddleware) authenticateService(ctx context.Context, c fiber.Ctx, token string) error {
	if token != a.serviceToken {
		return ErrInvalidServiceToken
	}
	ctx = context.WithValue(ctx, contexts.KeyUserID, a.appName)
	ctx = context.WithValue(ctx, contexts.KeyUsername, a.appName)
	ctx = context.WithValue(ctx, contexts.KeyUserRoles, []string{"service"})
	c.SetContext(ctx)
	c.Locals("roles", []string{"service"})
	a.log.InfoCtx(ctx, "service token authenticated", zap.String("service", a.appName))
	return c.Next()
}

func (a *AuthMiddleware) authenticateJWT(ctx context.Context, c fiber.Ctx) error {
	header := c.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ErrMissingOrMalformedToken
	}
	tokenStr := strings.TrimPrefix(header, "Bearer ")

	var mClaims gjwt.MapClaims
	allowExpired := a.env != "production"
	if err := a.verifier.Validate(ctx, tokenStr, &mClaims, allowExpired); err != nil {
		a.log.ErrorCtx(ctx, "jwt validation failed", zap.Error(err))
		switch {
		case errors.Is(err, gjwt.ErrTokenMalformed):
			return ErrTokenMalformed
		case errors.Is(err, gjwt.ErrTokenUnverifiable):
			return ErrTokenUnverifiable
		case errors.Is(err, gjwt.ErrTokenSignatureInvalid):
			return ErrInvalidSignature
		case errors.Is(err, gjwt.ErrTokenExpired):
			return ErrTokenExpired
		default:
			return ErrInvalidToken
		}
	}

	sub, _ := mClaims["sub"].(string)
	tid, _ := mClaims["tid"].(string)
	usern, _ := mClaims["preferred_username"].(string)

	var roles []string
	if ra, ok := mClaims["realm_access"].(map[string]interface{}); ok {
		if arr, ok := ra["roles"].([]interface{}); ok {
			for _, r := range arr {
				if s, ok := r.(string); ok {
					roles = append(roles, s)
				}
			}
		}
	}

	ctx = context.WithValue(ctx, contexts.KeyTenantID, tid)
	ctx = context.WithValue(ctx, contexts.KeyUserID, sub)
	ctx = context.WithValue(ctx, contexts.KeyUsername, usern)
	ctx = context.WithValue(ctx, contexts.KeyUserRoles, roles)
	c.SetContext(ctx)

	c.Locals("claims", mClaims)
	c.Locals("tenantID", tid)
	c.Locals("userID", sub)
	c.Locals("username", usern)
	c.Locals("roles", roles)

	a.log.InfoCtx(ctx, "jwt authenticated",
		zap.String("tenant", tid),
		zap.String("user", sub),
		zap.Strings("roles", roles),
	)
	return c.Next()
}
