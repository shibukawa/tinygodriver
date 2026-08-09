//go:build tinygo

package router

import (
	"unsafe"

	"github.com/shibukawa/tinygodriver/fasthttp"
)

// sameHandler reports whether a and b are the same function.
//
// Upstream compares reflect.ValueOf(h).Pointer(), which TinyGo implements
// unreliably for funcs: a handler stored into the radix tree and looked up
// again calls the right function, yet reflects to a pointer unrelated to the
// original's. TinyGo's func value is a {context, code} pair, and the context
// word is not even stable for non-capturing closures — the same handler has
// been observed with a static address, 0x1, and nil there. The code word is
// the function, so that is what gets compared. Closures over the same literal
// with different captured state would compare equal here; TestRouterMutable's
// handlers capture nothing, so the distinction cannot bite.
func sameHandler(a, b fasthttp.RequestHandler) bool {
	type funcValue struct {
		context unsafe.Pointer
		code    unsafe.Pointer
	}
	return (*funcValue)(unsafe.Pointer(&a)).code == (*funcValue)(unsafe.Pointer(&b)).code
}
