package http

import (
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	apperrors "github.com/shadowofcards/go-toolkit/errors"
)

var (
	ErrUnauthorized = apperrors.New().
			WithHTTPStatus(http.StatusUnauthorized).
			WithCode("unauthorized").
			WithMessage("unauthorized")

	ErrForbidden = apperrors.New().
			WithHTTPStatus(http.StatusForbidden).
			WithCode("forbidden").
			WithMessage("forbidden")
)

func RequirePermission(permission string) fiber.Handler {
	return func(c fiber.Ctx) error {
		raw := c.Locals("claims")
		claims, ok := raw.(jwt.MapClaims)
		if !ok {
			return ErrUnauthorized
		}
		permsRaw, _ := claims["perms"].([]interface{})
		for _, p := range permsRaw {
			if s, ok := p.(string); ok && s == permission {
				return c.Next()
			}
		}
		return ErrForbidden
	}
}
