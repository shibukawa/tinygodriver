//go:build !tinygo

package router

import (
	"reflect"

	"github.com/shibukawa/tinygodriver/fasthttp"
)

// sameHandler reports whether a and b are the same function value. On host Go
// this is upstream TestRouterMutable's own comparison, hoisted into a helper
// so the TinyGo build can substitute one that works there; see the tinygo
// variant for why reflect cannot be used on both compilers.
func sameHandler(a, b fasthttp.RequestHandler) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}
