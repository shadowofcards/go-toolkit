package http

import (
	"fmt"

	"github.com/gofiber/fiber/v3"
)

func RouterProtected(
	r fiber.Router,
	methodsArg interface{},
	path string,
	handler fiber.Handler,
	permissions ...any,
) fiber.Router {

	var methods []string
	switch m := methodsArg.(type) {
	case string:
		methods = []string{m}
	case []string:
		methods = m
	default:
		panic(fmt.Sprintf("RouterProtected: methods must be string or []string, got %T", methodsArg))
	}

	if len(permissions) == 0 {
		return r.Add(methods, path, handler)
	}

	permStrs := make([]string, len(permissions))
	for i, p := range permissions {
		permStrs[i] = fmt.Sprint(p)
	}

	mws := make([]fiber.Handler, 0, len(permStrs)+2)

	mws = append(mws, func(c fiber.Ctx) error {
		c.Locals("used_permissions", permStrs)
		return c.Next()
	})

	for _, perm := range permStrs {
		mws = append(mws, RequirePermission(perm))
	}

	mws = append(mws, handler)

	return r.Add(methods, path, mws[0], mws[1:]...)
}
