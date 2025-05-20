package middlewares

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"

	apperr "github.com/leandrodaf/go-toolkit/errors"
)

type errorPayload struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Context interface{} `json:"context,omitempty"`
}

func NewErrorHandler() fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		switch {
		case isValidation(err):
			return respond(c, fiber.StatusBadRequest, validationPayload(err))
		case isAppError(err):
			ae, _ := apperr.FromError(err)
			return respond(c, ae.Status(), errorPayload{Code: ae.ErrCode(), Message: ae.Message})
		case isFiberError(err):
			fe := err.(*fiber.Error)
			code := strings.ReplaceAll(strings.ToUpper(http.StatusText(fe.Code)), " ", "_")
			return respond(c, fe.Code, errorPayload{Code: code, Message: fe.Message})
		default:
			return respond(c, fiber.StatusInternalServerError, errorPayload{
				Code:    "INTERNAL_ERROR",
				Message: "internal server error",
			})
		}
	}
}

func isValidation(err error) bool {
	var ve validator.ValidationErrors
	return errors.As(err, &ve)
}

func validationPayload(err error) errorPayload {
	ve := err.(validator.ValidationErrors)
	ctx := make(map[string]string, len(ve))
	for _, f := range ve {
		ctx[f.Field()] = "validation failed on '" + f.Tag() + "'"
	}
	return errorPayload{Code: "VALIDATION_ERROR", Message: "validation failed", Context: ctx}
}

func isAppError(err error) bool {
	_, ok := apperr.FromError(err)
	return ok
}

func isFiberError(err error) bool {
	_, ok := err.(*fiber.Error)
	return ok
}

func respond(c fiber.Ctx, status int, p errorPayload) error {
	return c.Status(status).JSON(fiber.Map{"error": p})
}
