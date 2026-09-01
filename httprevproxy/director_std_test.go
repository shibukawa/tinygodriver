//go:build !tinygo && !force_tinygo_logic

package httprevproxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestDirectorPreservesInboundHost is the standard-Go half of the pair with
// director_tinygo_test.go. net/http dials Request.URL.Host and sends
// Request.Host as the Host header, so NewSingleHostReverseProxy can preserve
// the inbound Host exactly as it documents.
func TestDirectorPreservesInboundHost(t *testing.T) {
	backendAddr := backend(t)
	if backendAddr == "" {
		return
	}
	target, err := url.Parse("http://" + backendAddr)
	if err != nil {
		t.Fatalf("parse target: %v", err)
		return
	}

	ln, proxyAddr := listenSomewhere(t)
	if ln == nil {
		return
	}
	serveOn(t, ln, NewSingleHostReverseProxy(target))

	status, body, ok := get(t, proxyAddr, "/director")
	if !ok {
		return
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", status, body)
		return
	}
	// The inbound Host, not the target's: that is what "preserves" means.
	want := "backend host=" + proxyAddr + " path=/director"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
		return
	}
}

func TestNewSingleHostReverseProxyUsesDirectorCompatibility(t *testing.T) {
	target, _ := url.Parse("http://backend.example/base")
	proxy := NewSingleHostReverseProxy(target)
	if proxy.Director == nil || proxy.Rewrite != nil {
		t.Fatal("NewSingleHostReverseProxy must use Director for standard-library compatibility")
	}
	proxy.Transport = roundTripFunc(func(out *http.Request) (*http.Response, error) {
		if got, want := out.URL.String(), "http://backend.example/base/path"; got != want {
			t.Errorf("outbound URL = %q, want %q", got, want)
		}
		if got := out.Host; got != "frontend.example" {
			t.Errorf("outbound Host = %q", got)
		}
		if got := out.Header.Get("X-Forwarded-For"); got != "198.51.100.1, 192.0.2.20" {
			t.Errorf("X-Forwarded-For = %q", got)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "http://frontend.example/path", nil)
	request.RemoteAddr = "192.0.2.20:1000"
	request.Header.Set("X-Forwarded-For", "198.51.100.1")
	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Errorf("status = %d", recorder.Code)
	}
}
