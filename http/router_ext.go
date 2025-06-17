package http

import (
	"github.com/gofiber/fiber/v3"
)

func RegisterProtected(
	r fiber.Router,
	methods []string,
	path string,
	handler fiber.Handler,
	permissions ...string,
) fiber.Router {
	if len(permissions) == 0 {
		return r.Add(methods, path, handler)
	}

	var mws []fiber.Handler
	for _, perm := range permissions {
		mws = append(mws, RequirePermission(perm))
	}

	return r.Add(methods, path, handler, mws...)
}
