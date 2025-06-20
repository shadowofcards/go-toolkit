package middlewares

import (
	"errors"
	"net/http"

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

		caller := c.Get("X-Caller-ID", "external")
		method := string(c.Method())
		path := c.Route().Path
		if path == "" {
			path = c.OriginalURL()
		}

		tags := map[string]string{
			"method": method,
			"path":   path,
			"caller": caller,
		}

		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			tags["code"] = "VALIDATION_ERROR"
			tags["type"] = "validation"
			_ = rec.IncWithTags(ctx, "http_errors_total", 1, tags)

			context := make(map[string]string, len(ve))
			for _, f := range ve {
				context[f.Field()] = "validation failed on '" + f.Tag() + "'"
			}
			return respond(c, http.StatusBadRequest, errorPayload{
				Code:    "VALIDATION_ERROR",
				Message: "validation failed",
				Context: context,
			})
		}

		if ae, ok := apperr.FromError(err); ok {
			tags["code"] = ae.ErrCode()
			tags["type"] = "app_error"
			_ = rec.IncWithTags(ctx, "http_errors_total", 1, tags)

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
			tags["code"] = statusCodeKey(fe.Code)
			tags["type"] = "fiber_error"
			_ = rec.IncWithTags(ctx, "http_errors_total", 1, tags)

			return respond(c, fe.Code, errorPayload{
				Code:    tags["code"],
				Message: fe.Message,
			})
		}

		tags["code"] = "INTERNAL_ERROR"
		tags["type"] = "unknown"
		_ = rec.IncWithTags(ctx, "http_errors_total", 1, tags)

		return respond(c, http.StatusInternalServerError, errorPayload{
			Code:    "INTERNAL_ERROR",
			Message: "internal server error",
		})
	}
}

func respond(c fiber.Ctx, status int, p errorPayload) error {
	return c.Status(status).JSON(fiber.Map{"error": p})
}
