package middlewares

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	apperr "github.com/shadowofcards/go-toolkit/errors"
	"github.com/shadowofcards/go-toolkit/metrics"
)

type errorPayload struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Context interface{} `json:"context,omitempty"`
}

func NewErrorHandler(rec metrics.Recorder) fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		ctx := c.Context()

		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			code := "VALIDATION_ERROR"
			_ = rec.Inc(ctx, "http_error_handler_code_"+code, 1)
			_ = rec.Inc(ctx, "http_error_handler_type_"+code, 1)

			context := make(map[string]string, len(ve))
			for _, f := range ve {
				context[f.Field()] = "validation failed on '" + f.Tag() + "'"
			}
			return respond(c, http.StatusBadRequest, errorPayload{
				Code:    code,
				Message: "validation failed",
				Context: context,
			})
		}

		if ae, ok := apperr.FromError(err); ok {
			code := ae.ErrCode()
			typeName := "<nil>"
			if ae.Err != nil {
				typeName = fmt.Sprintf("%T", ae.Err)
			}

			_ = rec.Inc(ctx, "http_error_handler_code_"+code, 1)
			_ = rec.Inc(ctx, "http_error_handler_type_"+typeName, 1)

			payload := errorPayload{
				Code:    code,
				Message: ae.Message,
			}
			if len(ae.Context) > 0 {
				payload.Context = ae.Context
			}
			return respond(c, ae.Status(), payload)
		}

		if fe, ok := err.(*fiber.Error); ok {
			code := strings.ReplaceAll(strings.ToUpper(http.StatusText(fe.Code)), " ", "_")

			_ = rec.Inc(ctx, "http_error_handler_code_"+code, 1)
			_ = rec.Inc(ctx, "http_error_handler_type_*fiber.Error", 1)

			return respond(c, fe.Code, errorPayload{
				Code:    code,
				Message: fe.Message,
			})
		}

		code := "INTERNAL_ERROR"
		_ = rec.Inc(ctx, "http_error_handler_code_"+code, 1)
		_ = rec.Inc(ctx, "http_error_handler_type_unknown", 1)

		return respond(c, http.StatusInternalServerError, errorPayload{
			Code:    code,
			Message: "internal server error",
		})
	}
}

func respond(c fiber.Ctx, status int, p errorPayload) error {
	return c.Status(status).JSON(fiber.Map{"error": p})
}
