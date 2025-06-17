package http

import (
	"fmt"

	"github.com/gofiber/fiber/v3"
)

func RouterProteced(
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
		panic(fmt.Sprintf("RouterProteced: methods must be string or []string, got %T", methodsArg))
	}

	if len(permissions) == 0 {
		return r.Add(methods, path, handler)
	}

	mws := make([]fiber.Handler, 0, len(permissions)+1)
	for _, p := range permissions {
		permStr := fmt.Sprint(p)
		mws = append(mws, RequirePermission(permStr))
	}
	mws = append(mws, handler)

	return r.Add(methods, path, mws[0], mws[1:]...)
}
