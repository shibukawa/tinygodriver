//go:build !tinygo

// Connection reuse, asserted through the number of TCP connections the server
// accepts. These tests run unchanged on both paths: net/http pools on the
// standard-Go path and this package pools on the native one, so any divergence
// in observable behavior fails here rather than in a user's program.
package https_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/https"
)

// poolServer counts accepted connections, which is the only reliable evidence
// that a request reused one rather than dialing.
type poolServer struct {
	URL   string
	CAPEM []byte

	ln     *countingListener
	srv    *http.Server
	closer func()
}

type countingListener struct {
	net.Listener
	mu sync.Mutex
	n  int
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.n++
		l.mu.Unlock()
	}
	return conn, err
}

func (l *countingListener) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// newPoolServer starts a TLS server on 127.0.0.1 with a fresh CA. idleTimeout
// of zero leaves connections open indefinitely.
func newPoolServer(t *testing.T, idleTimeout time.Duration) *poolServer {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s:%s", r.Method, body)
	})
	mux.HandleFunc("/close", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		fmt.Fprint(w, "bye")
	})
	// This body is deliberately larger than any budget an implementation will
	// spend draining one the caller walked away from: net/http reads at most
	// 256 KiB (maxPostCloseReadBytes, since Go 1.27) and this package's pool at
	// most maxDrainBytes, and both then close the connection instead. A body
	// under whichever budget applies is drained and the connection kept, which
	// is the opposite answer, so the size is what keeps
	// TestAbandonedBodyNotReused meaning the same thing on both paths and on
	// either toolchain.
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 512<<10)))
	})
	mux.HandleFunc("/nocontent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/stall", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
	})

	base, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := &countingListener{Listener: base}
	srv := &http.Server{
		Handler:     mux,
		IdleTimeout: idleTimeout,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"http/1.1"},
		},
	}
	go srv.ServeTLS(ln, "", "")

	port := base.Addr().(*net.TCPAddr).Port
	return &poolServer{
		URL:    fmt.Sprintf("https://localhost:%d", port),
		CAPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		ln:     ln,
		srv:    srv,
		closer: func() { srv.Close() },
	}
}

func (s *poolServer) Close() { s.closer() }

// client builds a client trusting this server, letting the caller adjust the
// transport before it is used.
func (s *poolServer) client(t *testing.T, tune func(*https.Transport)) *http.Client {
	t.Helper()
	tr := https.NewTransport(https.WithRootCAPEM(s.CAPEM))
	if tune != nil {
		tune(tr)
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}

// getAndDrain performs a GET and reads the body to completion, which is the
// precondition for reuse.
func getAndDrain(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return string(body)
}

func TestConnectionReusedAcrossRequests(t *testing.T) {
	srv := newPoolServer(t, 0)
	defer srv.Close()
	c := srv.client(t, nil)

	for i := 0; i < 3; i++ {
		if body := getAndDrain(t, c, srv.URL+"/hello"); body != "hello" {
			t.Fatalf("request %d body = %q, want %q", i, body, "hello")
		}
	}
	if n := srv.ln.count(); n != 1 {
		t.Fatalf("accepted %d connections for 3 requests, want 1", n)
	}
}

func TestDisableKeepAlivesDialsEveryRequest(t *testing.T) {
	srv := newPoolServer(t, 0)
	defer srv.Close()
	c := srv.client(t, func(tr *https.Transport) { tr.DisableKeepAlives = true })

	for i := 0; i < 3; i++ {
		getAndDrain(t, c, srv.URL+"/hello")
	}
	if n := srv.ln.count(); n != 3 {
		t.Fatalf("accepted %d connections for 3 requests, want 3", n)
	}
}

func TestConnectionCloseResponseNotReused(t *testing.T) {
	srv := newPoolServer(t, 0)
	defer srv.Close()
	c := srv.client(t, nil)

	if body := getAndDrain(t, c, srv.URL+"/close"); body != "bye" {
		t.Fatalf("body = %q, want %q", body, "bye")
	}
	getAndDrain(t, c, srv.URL+"/hello")

	if n := srv.ln.count(); n != 2 {
		t.Fatalf("accepted %d connections, want 2: a Connection: close response must not be reused", n)
	}
}

func TestAbandonedBodyNotReused(t *testing.T) {
	srv := newPoolServer(t, 0)
	defer srv.Close()
	c := srv.client(t, nil)

	resp, err := c.Get(srv.URL + "/big")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// Read a token amount and walk away, leaving the connection mid-body. The
	// body is too large to drain, so no implementation can keep the connection.
	if _, err := io.CopyN(io.Discard, resp.Body, 16); err != nil {
		t.Fatalf("partial read: %v", err)
	}
	resp.Body.Close()

	getAndDrain(t, c, srv.URL+"/hello")
	if n := srv.ln.count(); n != 2 {
		t.Fatalf("accepted %d connections, want 2: an abandoned body must not leave a reusable connection", n)
	}
}

func TestBodylessResponsesAreReused(t *testing.T) {
	srv := newPoolServer(t, 0)
	defer srv.Close()
	c := srv.client(t, nil)

	// Neither response has a body to read, and the caller closes without
	// reading, which is the normal way these are used.
	resp, err := c.Head(srv.URL + "/hello")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	resp.Body.Close()

	resp, err = c.Get(srv.URL + "/nocontent")
	if err != nil {
		t.Fatalf("get 204: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()

	getAndDrain(t, c, srv.URL+"/hello")
	if n := srv.ln.count(); n != 1 {
		t.Fatalf("accepted %d connections for 3 requests, want 1", n)
	}
}

func TestIdleConnTimeoutExpiresConnection(t *testing.T) {
	srv := newPoolServer(t, 0)
	defer srv.Close()
	c := srv.client(t, func(tr *https.Transport) { tr.IdleConnTimeout = 20 * time.Millisecond })

	getAndDrain(t, c, srv.URL+"/hello")
	time.Sleep(200 * time.Millisecond)
	getAndDrain(t, c, srv.URL+"/hello")

	if n := srv.ln.count(); n != 2 {
		t.Fatalf("accepted %d connections, want 2: an expired idle connection must not be reused", n)
	}
}

func TestCloseIdleConnections(t *testing.T) {
	srv := newPoolServer(t, 0)
	defer srv.Close()

	tr := https.NewTransport(https.WithRootCAPEM(srv.CAPEM))
	c := &http.Client{Transport: tr}

	getAndDrain(t, c, srv.URL+"/hello")
	tr.CloseIdleConnections()
	getAndDrain(t, c, srv.URL+"/hello")

	if n := srv.ln.count(); n != 2 {
		t.Fatalf("accepted %d connections, want 2", n)
	}
}

// A server that drops idle connections is the case the pool cannot detect, so
// the request has to recover from it instead.
func TestStaleConnectionIsRetried(t *testing.T) {
	srv := newPoolServer(t, 50*time.Millisecond)
	defer srv.Close()
	c := srv.client(t, nil)

	getAndDrain(t, c, srv.URL+"/hello")
	// Outlive the server's idle timeout while staying well inside the client's,
	// so the pooled connection is dead but still considered fresh.
	time.Sleep(400 * time.Millisecond)

	if body := getAndDrain(t, c, srv.URL+"/hello"); body != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
	if n := srv.ln.count(); n != 2 {
		t.Fatalf("accepted %d connections, want 2", n)
	}
}

func TestStaleConnectionRetriesRequestWithBody(t *testing.T) {
	srv := newPoolServer(t, 50*time.Millisecond)
	defer srv.Close()
	c := srv.client(t, nil)

	getAndDrain(t, c, srv.URL+"/hello")
	time.Sleep(400 * time.Millisecond)

	// http.NewRequest gives a strings.Reader body a GetBody, which is what
	// makes the retry legal. The echoed body proves it was rebuilt intact.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/echo", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "POST:payload" {
		t.Fatalf("body = %q, want %q", body, "POST:payload")
	}
}

// A deadline belongs to one request. Carrying it onto a pooled connection makes
// the next request fail instantly for no reason the caller can see.
func TestDeadlineNotCarriedOntoPooledConnection(t *testing.T) {
	srv := newPoolServer(t, 0)
	defer srv.Close()
	c := srv.client(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	cancel()

	// Outlive the first request's deadline before reusing its connection.
	time.Sleep(600 * time.Millisecond)

	if body := getAndDrain(t, c, srv.URL+"/hello"); body != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
	if n := srv.ln.count(); n != 1 {
		t.Fatalf("accepted %d connections, want 1", n)
	}
}

func TestCancelledRequestLeavesNoReusableConnection(t *testing.T) {
	srv := newPoolServer(t, 0)
	defer srv.Close()
	c := srv.client(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stall", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected the stalled request to be cancelled")
	}

	if body := getAndDrain(t, c, srv.URL+"/hello"); body != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
	if n := srv.ln.count(); n != 2 {
		t.Fatalf("accepted %d connections, want 2: a cancelled request must not leave its connection behind", n)
	}
}
