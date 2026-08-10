//go:build !tinygo

// These tests run under host Go. Without tags they exercise the standard-Go
// delegation path; with -tags force_tinygo_logic they exercise the native
// backend, so the C code is testable without a TinyGo toolchain.
package https_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/https"
)

// Compile-time proof that the helpers match net/http signatures.
var (
	_ func(string) (*http.Response, error)                    = https.Get
	_ func(string) (*http.Response, error)                    = http.Get
	_ func(string) (*http.Response, error)                    = https.Head
	_ func(string, string, io.Reader) (*http.Response, error) = https.Post
	_ func(string, url.Values) (*http.Response, error)        = https.PostForm
	_ http.RoundTripper                                       = (*https.Transport)(nil)
)

type testServer struct {
	URL    string
	CAPEM  []byte
	Host   string
	closer func()
}

// newTestServer starts a TLS server with a freshly generated CA. notAfter and
// hostname are configurable so certificate failures can be tested.
func newTestServer(t *testing.T, hostname string, notAfter time.Time) *testServer {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: hostname},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{hostname},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(hostname); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
		tmpl.DNSNames = nil
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
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 200_000)))
	})
	mux.HandleFunc("/stall", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	})
	mux.HandleFunc("/slow-body", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(250 * time.Millisecond)
		fmt.Fprint(w, "late")
	})

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
			MinVersion:   tls.VersionTLS12,
			// The native backend speaks HTTP/1.1 only.
			NextProtos: []string{"http/1.1"},
		},
	}
	go srv.ServeTLS(ln, "", "")

	port := ln.Addr().(*net.TCPAddr).Port
	return &testServer{
		URL:    fmt.Sprintf("https://%s:%d", hostname, port),
		CAPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		Host:   hostname,
		closer: func() { srv.Close() },
	}
}

func (s *testServer) Close() { s.closer() }

func TestGetWithCustomCA(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	client := https.NewClient(https.WithRootCAPEM(srv.CAPEM))
	resp, err := client.Get(srv.URL + "/hello")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestResponseTimeoutIncludesBody(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	tr := https.NewTransport(https.WithRootCAPEM(srv.CAPEM))
	tr.ResponseTimeout = 50 * time.Millisecond
	client := &http.Client{Transport: tr}

	resp, err := client.Get(srv.URL + "/slow-body")
	if err != nil {
		// A backend may observe the timeout while still reading the headers.
		return
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("expected ResponseTimeout while reading the response body")
	}
}

func TestRootCAFromFile(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, srv.CAPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	client := https.NewClient(https.WithRootCAFile(path))
	resp, err := client.Get(srv.URL + "/hello")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
}

func TestUntrustedRootRejected(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	// No custom CA: the self-signed server must be rejected.
	client := https.NewClient()
	resp, err := client.Get(srv.URL + "/hello")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected an error for an untrusted self-signed certificate")
	}
	if !errors.Is(err, https.ErrUntrustedRoot) && !errors.Is(err, https.ErrCertificateInvalid) {
		t.Fatalf("error = %v, want ErrUntrustedRoot or ErrCertificateInvalid", err)
	}
}

func TestInsecureSkipVerify(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	client := https.NewClient(https.WithInsecureSkipVerify(true))
	resp, err := client.Get(srv.URL + "/hello")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
}

func TestHostnameMismatch(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	// Reach the same server through 127.0.0.1, which the cert does not cover.
	url := strings.Replace(srv.URL, "localhost", "127.0.0.1", 1)
	client := https.NewClient(https.WithRootCAPEM(srv.CAPEM))
	resp, err := client.Get(url + "/hello")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected a hostname mismatch error")
	}
	if !errors.Is(err, https.ErrHostnameMismatch) &&
		!errors.Is(err, https.ErrUntrustedRoot) &&
		!errors.Is(err, https.ErrCertificateInvalid) {
		t.Fatalf("error = %v, want a certificate error", err)
	}
}

func TestExpiredCertificate(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(-time.Minute))
	defer srv.Close()

	client := https.NewClient(https.WithRootCAPEM(srv.CAPEM))
	resp, err := client.Get(srv.URL + "/hello")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected an expired certificate error")
	}
	if !errors.Is(err, https.ErrCertificateExpired) &&
		!errors.Is(err, https.ErrUntrustedRoot) &&
		!errors.Is(err, https.ErrCertificateInvalid) {
		t.Fatalf("error = %v, want a certificate error", err)
	}
}

func TestPostForm(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	client := https.NewClient(https.WithRootCAPEM(srv.CAPEM))
	resp, err := client.PostForm(srv.URL+"/echo", url.Values{"a": {"1"}, "b": {"2"}})
	if err != nil {
		t.Fatalf("postform: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "POST:a=1&b=2" {
		t.Fatalf("body = %q, want %q", body, "POST:a=1&b=2")
	}
}

func TestPost(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	client := https.NewClient(https.WithRootCAPEM(srv.CAPEM))
	resp, err := client.Post(srv.URL+"/echo", "text/plain", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "POST:payload" {
		t.Fatalf("body = %q, want %q", body, "POST:payload")
	}
}

func TestHead(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	client := https.NewClient(https.WithRootCAPEM(srv.CAPEM))
	resp, err := client.Head(srv.URL + "/hello")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestLargeBody exercises the receive path across many records, including the
// leftover buffering in the native backend.
func TestLargeBody(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	client := https.NewClient(https.WithRootCAPEM(srv.CAPEM))
	resp, err := client.Get(srv.URL + "/big")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) != 200_000 {
		t.Fatalf("len(body) = %d, want 200000", len(body))
	}
}

func TestClientTimeout(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	client := https.NewClient(https.WithRootCAPEM(srv.CAPEM))
	client.Timeout = 300 * time.Millisecond

	start := time.Now()
	resp, err := client.Get(srv.URL + "/stall")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected a timeout")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %v, want well under the 5s handler sleep", elapsed)
	}
}

func TestConcurrentRequests(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	client := https.NewClient(https.WithRootCAPEM(srv.CAPEM))
	const n = 8
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			resp, err := client.Get(srv.URL + "/hello")
			if err != nil {
				errCh <- err
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			errCh <- nil
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent request %d: %v", i, err)
		}
	}
}

func TestRootCAsOnlyRejectsSystemAnchors(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	// An unrelated CA plus RootCAsOnly must not trust this server.
	other := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer other.Close()

	client := https.NewClient(
		https.WithRootCAPEM(other.CAPEM),
		https.WithRootCAsOnly(true),
	)
	resp, err := client.Get(srv.URL + "/hello")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected rejection when only an unrelated CA is trusted")
	}
}

// TestRootCAsOnlyWithNoAnchors pins that an empty trust set rejects everything.
// The darwin backend used to fall back to the system store here, because
// SecTrustSetAnchorCertificates treats a NULL array as "restore the defaults".
func TestRootCAsOnlyWithNoAnchors(t *testing.T) {
	srv := newTestServer(t, "localhost", time.Now().Add(time.Hour))
	defer srv.Close()

	client := https.NewClient(https.WithRootCAsOnly(true))
	resp, err := client.Get(srv.URL + "/hello")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected rejection: RootCAsOnly with no anchors must trust nothing")
	}
}

func TestBadPEMReported(t *testing.T) {
	client := https.NewClient(https.WithRootCAPEM([]byte("not a pem")))
	_, err := client.Get("https://localhost:1/hello")
	if err == nil {
		t.Fatal("expected an error for malformed PEM")
	}
}
