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

		caller := c.Get("X-Caller-ID", "external")
		method := string(c.Method())
		path := c.Route().Path
		if path == "" {
			path = c.OriginalURL()
		}

		baseTags := map[string]string{
			"method": method,
			"path":   path,
			"caller": caller,
		}

		// Validação com validator.v10
		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			code := "VALIDATION_ERROR"
			tags := cloneTags(baseTags)
			tags["code"] = code
			tags["type"] = "validation"

			_ = rec.IncWithTags(ctx, "http_errors_total", 1, tags)

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

		// AppError customizado
		if ae, ok := apperr.FromError(err); ok {
			code := ae.ErrCode()
			typeName := "<nil>"
			if ae.Err != nil {
				typeName = fmt.Sprintf("%T", ae.Err)
			}

			tags := cloneTags(baseTags)
			tags["code"] = code
			tags["type"] = typeName

			_ = rec.IncWithTags(ctx, "http_errors_total", 1, tags)

			payload := errorPayload{
				Code:    code,
				Message: ae.Message,
			}
			if len(ae.Context) > 0 {
				payload.Context = ae.Context
			}
			return respond(c, ae.Status(), payload)
		}

		// Erro do Fiber
		if fe, ok := err.(*fiber.Error); ok {
			code := strings.ReplaceAll(strings.ToUpper(http.StatusText(fe.Code)), " ", "_")
			tags := cloneTags(baseTags)
			tags["code"] = code
			tags["type"] = "*fiber.Error"

			_ = rec.IncWithTags(ctx, "http_errors_total", 1, tags)

			return respond(c, fe.Code, errorPayload{
				Code:    code,
				Message: fe.Message,
			})
		}

		// Erro desconhecido
		code := "INTERNAL_ERROR"
		tags := cloneTags(baseTags)
		tags["code"] = code
		tags["type"] = "unknown"

		_ = rec.IncWithTags(ctx, "http_errors_total", 1, tags)

		return respond(c, http.StatusInternalServerError, errorPayload{
			Code:    code,
			Message: "internal server error",
		})
	}
}

func respond(c fiber.Ctx, status int, p errorPayload) error {
	return c.Status(status).JSON(fiber.Map{"error": p})
}
