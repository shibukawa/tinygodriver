package httprevproxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/shibukawa/tinygodriver/netdev"
)

// The tests in this file proxy over real sockets rather than through a
// RoundTripper stub, because the defect they guard lives in the transport:
// TinyGo dials Request.Host where standard net/http dials Request.URL.Host.
// A stubbed RoundTripper never dials, so it cannot see the difference.
//
// Two TinyGo facts shape them, per rule:tinygo-test-constraints. Listener.Addr
// reports port 0 under netdev, so listenSomewhere picks the port itself; and
// t.Fatalf does not stop a TinyGo test, so every Fatalf is followed by an
// explicit return.

var nextPort int32 = 19900

func listenSomewhere(t *testing.T) (net.Listener, string) {
	t.Helper()
	for i := 0; i < 64; i++ {
		addr := "127.0.0.1:" + strconv.Itoa(int(atomic.AddInt32(&nextPort, 1)))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, addr
		}
	}
	t.Errorf("no free port in range")
	return nil, ""
}

// serveOn runs srv on ln until the test ends.
func serveOn(t *testing.T, ln net.Listener, h http.Handler) {
	t.Helper()
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close() })
}

// backend answers with the Host header it received, so a test can tell a
// correctly routed request from one that arrived somewhere else.
func backend(t *testing.T) string {
	t.Helper()
	ln, addr := listenSomewhere(t)
	if ln == nil {
		return ""
	}
	serveOn(t, ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "backend host=%s path=%s", r.Host, r.URL.Path)
	}))
	return addr
}

func get(t *testing.T, addr, path string) (int, string, bool) {
	t.Helper()
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + addr + path)
	if err != nil {
		t.Errorf("GET %s%s: %v", addr, path, err)
		return 0, "", false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Errorf("read body: %v", err)
		return 0, "", false
	}
	return resp.StatusCode, string(body), true
}

// TestRewriteSetURLReachesTarget is the regression test for the outbound Host
// compensation. SetURL clears Out.Host, which standard net/http reads as "take
// the Host header from the URL" and TinyGo reads as an empty dial address.
// Without fixOutboundHost this fails under TinyGo with "invalid IP address"
// and a 502, while passing under standard Go, so only a tinygo run catches it.
func TestRewriteSetURLReachesTarget(t *testing.T) {
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
	serveOn(t, ln, &ReverseProxy{
		Rewrite: func(r *ProxyRequest) { r.SetURL(target) },
	})

	status, body, ok := get(t, proxyAddr, "/hello")
	if !ok {
		return
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", status, body)
		return
	}
	want := "backend host=" + backendAddr + " path=/hello"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
		return
	}
}

// TestRewriteExplicitOutHostIsHonored covers the case the compensation must not
// touch: a Rewrite that sets Out.Host deliberately keeps it.
func TestRewriteExplicitOutHostIsHonored(t *testing.T) {
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
	serveOn(t, ln, &ReverseProxy{
		Rewrite: func(r *ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = target.Host
		},
	})

	status, body, ok := get(t, proxyAddr, "/explicit")
	if !ok {
		return
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", status, body)
		return
	}
	want := "backend host=" + backendAddr + " path=/explicit"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
		return
	}
}
