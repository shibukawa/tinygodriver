package httpmux

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestServeMuxRouting(t *testing.T) {
	mux := NewServeMux()
	registerText := func(pattern, text string, names ...string) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, text)
			for _, name := range names {
				_, _ = fmt.Fprintf(w, " %s=%s", name, r.PathValue(name))
			}
		})
	}
	registerText("/", "root")
	registerText("GET /items/{id}", "get", "id")
	registerText("HEAD /items/{id}", "head", "id")
	registerText("POST /items/{id}", "post", "id")
	registerText("GET /assets/{rest...}", "assets", "rest")
	registerText("GET example.com/host", "host")
	registerText("GET /exact/{$}", "exact")
	registerText("/literal/a%2Fb", "escaped")
	registerText("CONNECT /connect/../raw", "connect")
	registerText("CONNECT /tunnel/", "tunnel")

	tests := []struct {
		name   string
		method string
		target string
		host   string
		status int
		body   string
	}{
		{name: "single wildcard", method: "GET", target: "/items/a%2Fb", status: 200, body: "get id=a/b"},
		{name: "HEAD precedence", method: "HEAD", target: "/items/42", status: 200, body: "head id=42"},
		{name: "method", method: "POST", target: "/items/42", status: 200, body: "post id=42"},
		{name: "multi wildcard", method: "GET", target: "/assets/css%2Fmain/site.css", status: 200, body: "assets rest=css/main/site.css"},
		{name: "empty multi wildcard", method: "GET", target: "/assets/", status: 200, body: "assets rest="},
		{name: "host with stripped port", method: "GET", target: "/host", host: "example.com:8080", status: 200, body: "host"},
		{name: "host fallback", method: "GET", target: "/host", host: "other.example", status: 200, body: "root"},
		{name: "end marker", method: "GET", target: "/exact/", status: 200, body: "exact"},
		{name: "end marker excludes child", method: "GET", target: "/exact/child", status: 200, body: "root"},
		{name: "escaped literal", method: "PATCH", target: "/literal/a%2Fb", status: 200, body: "escaped"},
		{name: "CONNECT path is not cleaned", method: "CONNECT", target: "/connect/../raw", status: 200, body: "connect"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.target, nil)
			if test.host != "" {
				req.Host = test.host
			}
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != test.status || rr.Body.String() != test.body {
				t.Fatalf("got status %d body %q, want status %d body %q", rr.Code, rr.Body.String(), test.status, test.body)
			}
		})
	}

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("CONNECT", "/tunnel", nil))
	if rr.Code != http.StatusTemporaryRedirect || rr.Header().Get("Location") != "/tunnel/" {
		t.Fatalf("CONNECT slash redirect: got status %d Location %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestMethodNotAllowed(t *testing.T) {
	mux := NewServeMux()
	mux.HandleFunc("GET /resource", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /resource", func(http.ResponseWriter, *http.Request) {})

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("PUT", "/resource", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != "GET, HEAD, POST" {
		t.Fatalf("Allow = %q, want %q", got, "GET, HEAD, POST")
	}
}

func TestRedirects(t *testing.T) {
	mux := NewServeMux()
	mux.HandleFunc("GET /tree/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	tests := []struct {
		target   string
		location string
	}{
		{target: "/tree?x=1", location: "/tree/?x=1"},
		{target: "//tree//child?x=1", location: "/tree/child?x=1"},
	}
	for _, test := range tests {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("GET", test.target, nil))
		if rr.Code != http.StatusTemporaryRedirect || rr.Header().Get("Location") != test.location {
			t.Errorf("%q: got status %d Location %q", test.target, rr.Code, rr.Header().Get("Location"))
		}
	}

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("POST", "/tree", nil))
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST /tree: got status %d Allow %q", rr.Code, rr.Header().Get("Allow"))
	}
}

func TestHandlerDoesNotSetPathValue(t *testing.T) {
	mux := NewServeMux()
	wantHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mux.Handle("GET /users/{id}", wantHandler)
	req := httptest.NewRequest("GET", "/users/123", nil)

	h, pattern := mux.Handler(req)
	if h == nil || pattern != "GET /users/{id}" {
		t.Fatalf("Handler returned (%v, %q)", h, pattern)
	}
	if got := req.PathValue("id"); got != "" {
		t.Fatalf("Handler populated PathValue: %q", got)
	}
}

func TestRegistrationPanics(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		nilFunc  bool
	}{
		{name: "empty", patterns: []string{""}},
		{name: "missing slash", patterns: []string{"GET users"}},
		{name: "unclean", patterns: []string{"GET /a/../b"}},
		{name: "partial wildcard", patterns: []string{"/a{x}"}},
		{name: "non-terminal multi", patterns: []string{"/{x...}/b"}},
		{name: "non-terminal end", patterns: []string{"/{$}/b"}},
		{name: "duplicate wildcard", patterns: []string{"/{x}/{x}"}},
		{name: "equivalent", patterns: []string{"/a/{x}", "/a/{y}"}},
		{name: "overlap", patterns: []string{"GET /", "/index.html"}},
		{name: "nil func", patterns: []string{"/"}, nilFunc: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("registration did not panic")
				}
			}()
			mux := NewServeMux()
			for _, pattern := range test.patterns {
				if test.nilFunc {
					var f func(http.ResponseWriter, *http.Request)
					mux.HandleFunc(pattern, f)
				} else {
					mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
				}
			}
		})
	}
}

func TestSpecificityIndependentOfRegistrationOrder(t *testing.T) {
	patterns := []struct{ pattern, response string }{
		{pattern: "/", response: "root"},
		{pattern: "/images/", response: "images"},
		{pattern: "/images/thumbnails/", response: "thumbnails"},
		{pattern: "GET /images/thumbnails/{name}", response: "named"},
	}
	for _, reverse := range []bool{false, true} {
		mux := NewServeMux()
		for n := range patterns {
			i := n
			if reverse {
				i = len(patterns) - 1 - n
			}
			entry := patterns[i]
			mux.HandleFunc(entry.pattern, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, entry.response)
			})
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("GET", "/images/thumbnails/a", nil))
		if rr.Body.String() != "named" {
			t.Fatalf("reverse=%v: response = %q", reverse, rr.Body.String())
		}
	}
}

func TestAsteriskRequest(t *testing.T) {
	mux := NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(299) })
	req := httptest.NewRequest("OPTIONS", "http://example.com/", nil)
	req.RequestURI = "*"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.EqualFold(rr.Header().Get("Connection"), "close") {
		t.Fatalf("got status %d Connection %q", rr.Code, rr.Header().Get("Connection"))
	}
}

func TestConcurrentDispatch(t *testing.T) {
	mux := NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.PathValue("id"))
	})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest("GET", fmt.Sprintf("/users/%d", id), nil))
			if rr.Code != 200 || rr.Body.String() != fmt.Sprint(id) {
				t.Errorf("id %d: status %d body %q", id, rr.Code, rr.Body.String())
			}
		}(i)
	}
	wg.Wait()
}
