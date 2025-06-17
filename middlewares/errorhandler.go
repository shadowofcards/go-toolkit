package middlewares

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	apperr "github.com/shadowofcards/go-toolkit/errors"
)

type errorPayload struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Context interface{} `json:"context,omitempty"`
}

func NewErrorHandler() fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			ctx := make(map[string]string, len(ve))
			for _, f := range ve {
				ctx[f.Field()] = "validation failed on '" + f.Tag() + "'"
			}
			return respond(c, http.StatusBadRequest, errorPayload{
				Code:    "VALIDATION_ERROR",
				Message: "validation failed",
				Context: ctx,
			})
		}

		if ae, ok := apperr.FromError(err); ok {
			payload := errorPayload{
				Code:    ae.ErrCode(),
				Message: ae.Message,
			}
			if len(ae.Context) > 0 {
				payload.Context = ae.Context
			}
			return respond(c, ae.Status(), payload)
		}

		if fe, ok := err.(*fiber.Error); ok {
			code := strings.ReplaceAll(strings.ToUpper(http.StatusText(fe.Code)), " ", "_")
			return respond(c, fe.Code, errorPayload{
				Code:    code,
				Message: fe.Message,
			})
		}

		return respond(c, http.StatusInternalServerError, errorPayload{
			Code:    "INTERNAL_ERROR",
			Message: "internal server error",
		})
	}
}

func respond(c fiber.Ctx, status int, p errorPayload) error {
	return c.Status(status).JSON(fiber.Map{"error": p})
}
