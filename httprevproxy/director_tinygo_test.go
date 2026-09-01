//go:build tinygo || force_tinygo_logic

package httprevproxy

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestDirectorHostMismatchIsReported is the TinyGo half of the pair with
// director_std_test.go. TinyGo dials Request.Host, so preserving the inbound
// Host means dialing it — NewSingleHostReverseProxy would send the proxy back
// to its own address and loop until the header limit trips, which is what it
// did before fixOutboundHost existed. It must report instead.
func TestDirectorHostMismatchIsReported(t *testing.T) {
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
	got := make(chan string, 1)
	proxy := NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		select {
		case got <- err.Error():
		default:
		}
		w.WriteHeader(http.StatusBadGateway)
	}
	serveOn(t, ln, proxy)

	status, _, ok := get(t, proxyAddr, "/director")
	if !ok {
		return
	}
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", status)
		return
	}
	select {
	case msg := <-got:
		if !strings.Contains(msg, "cannot differ from the target URL host") {
			t.Fatalf("error = %q, want the Host/URL mismatch report", msg)
			return
		}
	default:
		t.Fatalf("ErrorHandler was not called")
		return
	}
}
