//go:build (tinygo || force_tinygo_logic) && !tinygo && (darwin || linux || windows)

package https

import (
	"bufio"
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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A minimal CONNECT proxy. It is deliberately hand-rolled rather than built on
// httputil, so the test exercises the same byte-level exchange a real proxy
// performs, including the requirement that nothing past the blank line is
// consumed before the tunnel opens.
type connectProxy struct {
	Host     string
	Port     string
	requests atomic.Int32
	lastAuth atomic.Value // string
	reject   int          // when non-zero, answer with this status instead
	close    func()
}

func newConnectProxy(t *testing.T, reject int) *connectProxy {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	p := &connectProxy{
		Host:   "127.0.0.1",
		Port:   strconv.Itoa(addr.Port),
		reject: reject,
		close:  func() { ln.Close() },
	}
	p.lastAuth.Store("")

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(c)
		}
	}()
	return p
}

func (p *connectProxy) serve(c net.Conn) {
	defer c.Close()

	// Read the request head one byte at a time, for the same reason the client
	// does: anything read past the terminator belongs to the tunnel.
	var head strings.Builder
	buf := make([]byte, 1)
	for !strings.HasSuffix(head.String(), "\r\n\r\n") {
		if head.Len() > 8192 {
			return
		}
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		if n > 0 {
			head.WriteByte(buf[0])
		}
	}
	p.requests.Add(1)

	lines := strings.Split(head.String(), "\r\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "CONNECT ") {
		fmt.Fprint(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}
	for _, l := range lines[1:] {
		if v, ok := strings.CutPrefix(l, "Proxy-Authorization: "); ok {
			p.lastAuth.Store(v)
		}
	}

	if p.reject != 0 {
		fmt.Fprintf(c, "HTTP/1.1 %d Nope\r\nContent-Length: 0\r\n\r\n", p.reject)
		return
	}

	target := strings.Fields(lines[0])[1]
	up, err := net.DialTimeout("tcp4", target, 5*time.Second)
	if err != nil {
		fmt.Fprint(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer up.Close()

	// A real proxy sends headers here; include one to prove the client skips
	// them rather than assuming a bare status line.
	fmt.Fprint(c, "HTTP/1.1 200 Connection established\r\nProxy-Agent: test\r\n\r\n")

	go io.Copy(up, c)
	io.Copy(c, up)
}

// originServer is an https server with its own CA, so the test proves the
// tunnel carries a real end-to-end TLS session rather than terminating at the
// proxy.
func newOriginServer(t *testing.T) (host, port string, caPEM []byte, stop func()) {
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
		fmt.Fprint(w, "through-the-tunnel")
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
			NextProtos:   []string{"http/1.1"},
		},
	}
	go srv.ServeTLS(ln, "", "")

	return "localhost", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		func() { srv.Close() }
}

func TestDialTLSThroughProxy(t *testing.T) {
	host, port, caPEM, stop := newOriginServer(t)
	defer stop()
	p := newConnectProxy(t, 0)
	defer p.close()

	t.Setenv("HTTPS_PROXY", "http://"+net.JoinHostPort(p.Host, p.Port))
	t.Setenv("NO_PROXY", "")

	client := NewClient(WithRootCAPEM(caPEM))
	resp, err := client.Get(fmt.Sprintf("https://%s/hello", net.JoinHostPort(host, port)))
	if err != nil {
		t.Fatalf("get through proxy: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "through-the-tunnel" {
		t.Fatalf("body = %q", body)
	}
	if n := p.requests.Load(); n != 1 {
		t.Fatalf("proxy saw %d CONNECT requests, want 1", n)
	}
}

// The certificate is verified against the origin's name, not the proxy's, so a
// tunnel cannot be used to slip a different peer in.
func TestProxyStillVerifiesOrigin(t *testing.T) {
	host, port, _, stop := newOriginServer(t)
	defer stop()
	p := newConnectProxy(t, 0)
	defer p.close()

	t.Setenv("HTTPS_PROXY", "http://"+net.JoinHostPort(p.Host, p.Port))
	t.Setenv("NO_PROXY", "")

	// No CA configured, so the self-signed origin must be rejected.
	client := NewClient()
	if resp, err := client.Get(fmt.Sprintf("https://%s/hello", net.JoinHostPort(host, port))); err == nil {
		resp.Body.Close()
		t.Fatal("expected the untrusted origin certificate to be rejected")
	}
}

func TestProxyAuthorizationSent(t *testing.T) {
	host, port, caPEM, stop := newOriginServer(t)
	defer stop()
	p := newConnectProxy(t, 0)
	defer p.close()

	t.Setenv("HTTPS_PROXY", "http://u:pw@"+net.JoinHostPort(p.Host, p.Port))
	t.Setenv("NO_PROXY", "")

	client := NewClient(WithRootCAPEM(caPEM))
	resp, err := client.Get(fmt.Sprintf("https://%s/hello", net.JoinHostPort(host, port)))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	if got := p.lastAuth.Load().(string); got != "Basic dTpwdw==" {
		t.Fatalf("Proxy-Authorization = %q", got)
	}
}

func TestProxyRefusalReported(t *testing.T) {
	host, port, caPEM, stop := newOriginServer(t)
	defer stop()
	p := newConnectProxy(t, 407)
	defer p.close()

	t.Setenv("HTTPS_PROXY", "http://"+net.JoinHostPort(p.Host, p.Port))
	t.Setenv("NO_PROXY", "")

	client := NewClient(WithRootCAPEM(caPEM))
	resp, err := client.Get(fmt.Sprintf("https://%s/hello", net.JoinHostPort(host, port)))
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected the 407 to surface")
	}
	if !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("error = %v, want it to name proxy authentication", err)
	}
}

// NO_PROXY must bypass the tunnel entirely, which is how an internal host stays
// reachable on a network where the proxy cannot see it.
func TestNoProxyBypassesTunnel(t *testing.T) {
	host, port, caPEM, stop := newOriginServer(t)
	defer stop()
	// Point at a closed port: if the client consults it at all, the test fails.
	p := newConnectProxy(t, 0)
	p.close()

	t.Setenv("HTTPS_PROXY", "http://"+net.JoinHostPort(p.Host, p.Port))
	t.Setenv("NO_PROXY", "localhost")

	client := NewClient(WithRootCAPEM(caPEM))
	resp, err := client.Get(fmt.Sprintf("https://%s/hello", net.JoinHostPort(host, port)))
	if err != nil {
		t.Fatalf("direct connection for a NO_PROXY host: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "through-the-tunnel" {
		t.Fatalf("body = %q", body)
	}
}

// An http:// request through a proxy uses the absolute request form rather
// than a tunnel, which is a different code path from CONNECT.
func TestPlainHTTPThroughProxy(t *testing.T) {
	var gotLine atomic.Value
	gotLine.Store("")

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				gotLine.Store(strings.TrimSpace(line))
				for {
					l, err := br.ReadString('\n')
					if err != nil || strings.TrimSpace(l) == "" {
						break
					}
				}
				fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nhi")
			}(c)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	t.Setenv("HTTP_PROXY", "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.Port)))
	t.Setenv("NO_PROXY", "")

	resp, err := NewClient().Get("http://example.invalid/path")
	if err != nil {
		t.Fatalf("plain http through proxy: %v", err)
	}
	defer resp.Body.Close()

	if got := gotLine.Load().(string); got != "GET http://example.invalid/path HTTP/1.1" {
		t.Fatalf("request line = %q, want the absolute form", got)
	}
}
