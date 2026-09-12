package router

import (
	"reflect"
	"testing"

	"github.com/savsgio/gotils/strings"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

func TestGetOptionalPath(t *testing.T) {
	handler := func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(fasthttp.StatusOK)
	}

	expected := []struct {
		path    string
		tsr     bool
		handler fasthttp.RequestHandler
	}{
		{"/show/{name}", false, handler},
		{"/show/{name}/", true, nil},
		{"/show/{name}/{surname}", false, handler},
		{"/show/{name}/{surname}/", true, nil},
		{"/show/{name}/{surname}/at", false, handler},
		{"/show/{name}/{surname}/at/", true, nil},
		{"/show/{name}/{surname}/at/{address}", false, handler},
		{"/show/{name}/{surname}/at/{address}/", true, nil},
		{"/show/{name}/{surname}/at/{address}/{id}", false, handler},
		{"/show/{name}/{surname}/at/{address}/{id}/", true, nil},
		{"/show/{name}/{surname}/at/{address}/{id}/{phone:.*}", false, handler},
		{"/show/{name}/{surname}/at/{address}/{id}/{phone:.*}/", true, nil},
	}

	r := New()
	r.GET("/show/{name}/{surname?}/at/{address?}/{id}/{phone?:.*}", handler)

	for _, e := range expected {
		ctx := new(fasthttp.RequestCtx)

		h, tsr := r.Lookup("GET", e.path, ctx)

		if tsr != e.tsr {
			t.Errorf("TSR (path: %s) == %v, want %v", e.path, tsr, e.tsr)
		}

		if reflect.ValueOf(h).Pointer() != reflect.ValueOf(e.handler).Pointer() {
			t.Errorf("Handler (path: %s) == %p, want %p", e.path, h, e.handler)
		}
	}

	tests := []struct {
		path          string
		optionalPaths []string
	}{
		{"/hello", nil},
		{"/{name}", nil},
		{"/{name?:[a-zA-Z]{5}}", []string{"/", "/{name:[a-zA-Z]{5}}"}},
		{"/{filepath:^(?!api).*}", nil},
		{"/static/{filepath?:^(?!api).*}", []string{"/static", "/static/{filepath:^(?!api).*}"}},
		{"/show/{name?}", []string{"/show", "/show/{name}"}},
	}

	for _, test := range tests {
		optionalPaths := getOptionalPaths(test.path)

		if len(optionalPaths) != len(test.optionalPaths) {
			t.Errorf("getOptionalPaths() len == %d, want %d", len(optionalPaths), len(test.optionalPaths))
		}

		for _, wantPath := range test.optionalPaths {
			if !strings.Include(optionalPaths, wantPath) {
				t.Errorf("The optional path is not returned for '%s': %s", test.path, wantPath)
			}
		}
	}
}
