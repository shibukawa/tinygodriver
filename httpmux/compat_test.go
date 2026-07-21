package httpmux

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

// This test runs the same fixture through the standard mux and this package.
// It intentionally compares observable routing behavior rather than internal
// implementation details.
func TestStandardServeMuxParity(t *testing.T) {
	requireHostGo(t)
	patterns := []struct {
		pattern string
		name    string
		values  []string
	}{
		{pattern: "GET /items/{id}", name: "get-item", values: []string{"id"}},
		{pattern: "HEAD /items/{id}", name: "head-item", values: []string{"id"}},
		{pattern: "POST /items/{id}", name: "post-item", values: []string{"id"}},
		{pattern: "GET /assets/{rest...}", name: "assets", values: []string{"rest"}},
		{pattern: "GET /tree/", name: "tree"},
		{pattern: "GET example.com/host", name: "host"},
		{pattern: "GET /exact/{$}", name: "exact"},
		{pattern: "/literal/a%2Fb", name: "literal"},
		{pattern: "CONNECT /connect/../raw", name: "connect"},
		{pattern: "CONNECT /tunnel/", name: "tunnel"},
	}

	standard := http.NewServeMux()
	compatible := NewServeMux()
	for _, fixture := range patterns {
		fixture := fixture
		handler := func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, fixture.name)
			for _, name := range fixture.values {
				_, _ = fmt.Fprintf(w, " %s=%s", name, r.PathValue(name))
			}
		}
		standard.HandleFunc(fixture.pattern, handler)
		compatible.HandleFunc(fixture.pattern, handler)
	}

	requests := []struct {
		method string
		target string
		host   string
	}{
		{method: "GET", target: "/items/a%2Fb"},
		{method: "HEAD", target: "/items/42"},
		{method: "POST", target: "/items/42"},
		{method: "DELETE", target: "/items/42"},
		{method: "GET", target: "/assets/css%2Fmain/site.css"},
		{method: "GET", target: "/assets/"},
		{method: "GET", target: "/tree"},
		{method: "GET", target: "/tree//child?x=1"},
		{method: "POST", target: "/tree"},
		{method: "GET", target: "/host", host: "example.com:8080"},
		{method: "GET", target: "/host", host: "other.example"},
		{method: "GET", target: "/exact/"},
		{method: "GET", target: "/exact/child"},
		{method: "PATCH", target: "/literal/a%2Fb"},
		{method: "CONNECT", target: "/connect/../raw"},
		{method: "CONNECT", target: "/tunnel"},
	}

	for _, fixture := range requests {
		name := fixture.method + " " + fixture.target
		if fixture.host != "" {
			name += " host=" + fixture.host
		}
		t.Run(name, func(t *testing.T) {
			standardRequest := httptest.NewRequest(fixture.method, fixture.target, nil)
			compatibleRequest := httptest.NewRequest(fixture.method, fixture.target, nil)
			if fixture.host != "" {
				standardRequest.Host = fixture.host
				compatibleRequest.Host = fixture.host
			}

			standardResponse := httptest.NewRecorder()
			compatibleResponse := httptest.NewRecorder()
			standard.ServeHTTP(standardResponse, standardRequest)
			compatible.ServeHTTP(compatibleResponse, compatibleRequest)

			if compatibleResponse.Code != standardResponse.Code ||
				compatibleResponse.Body.String() != standardResponse.Body.String() ||
				compatibleResponse.Header().Get("Location") != standardResponse.Header().Get("Location") ||
				compatibleResponse.Header().Get("Allow") != standardResponse.Header().Get("Allow") {
				t.Fatalf("standard=(%d, %q, Location=%q, Allow=%q), compatible=(%d, %q, Location=%q, Allow=%q)",
					standardResponse.Code, standardResponse.Body.String(), standardResponse.Header().Get("Location"), standardResponse.Header().Get("Allow"),
					compatibleResponse.Code, compatibleResponse.Body.String(), compatibleResponse.Header().Get("Location"), compatibleResponse.Header().Get("Allow"))
			}
		})
	}
}

func TestPatternValidationParity(t *testing.T) {
	requireHostGo(t)
	patterns := []string{
		"", "GET users", "GE T /", "GET /a/../b", "/a{x}", "/{x...}/b",
		"/{$}/b", "/{x}/{x}", "/{9x}", "/{}", "GET\n /",
	}
	for _, pattern := range patterns {
		t.Run(fmt.Sprintf("%q", pattern), func(t *testing.T) {
			standardPanicked := registrationPanics(func() {
				http.NewServeMux().HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
			})
			compatiblePanicked := registrationPanics(func() {
				NewServeMux().HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
			})
			if compatiblePanicked != standardPanicked {
				t.Fatalf("standard panic=%v, compatible panic=%v", standardPanicked, compatiblePanicked)
			}
		})
	}
}

func requireHostGo(t *testing.T) {
	t.Helper()
	if runtime.Compiler == "tinygo" {
		t.Skip("TinyGo's incomplete standard ServeMux is the reason this compatibility package exists")
	}
}

func registrationPanics(f func()) (panicked bool) {
	defer func() {
		panicked = recover() != nil
	}()
	f()
	return false
}
