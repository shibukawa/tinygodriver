//go:build !tinygo

// These run on both the std-go and the native backend, because DialPlain and
// Upgrade are the exported, cross-compiler API. That is the point of the pair:
// a driver written against them is shared between compilers.
package https

import (
	"bufio"
	"context"
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
	"strconv"
	"strings"
	"testing"
	"time"
)

// startTLSServer speaks a tiny in-band upgrade protocol before switching to
// TLS, in the same shape as PostgreSQL's SSLRequest and MySQL's capability
// exchange: a plaintext request, a one-token reply, then the handshake on the
// very same socket.
type startTLSServer struct {
	Host  string
	Port  string
	CAPEM []byte
	close func()
}

func newStartTLSServer(t *testing.T) *startTLSServer {
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
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()

				// Plaintext phase. Read exactly the request line and nothing
				// more: over-reading here would swallow the first TLS record,
				// which is the classic STARTTLS bug.
				line, err := readLine(c)
				if err != nil || line != "STARTTLS" {
					return
				}
				if _, err := c.Write([]byte("OK\n")); err != nil {
					return
				}

				// Same socket, now TLS.
				tc := tls.Server(c, tlsCfg)
				if err := tc.Handshake(); err != nil {
					return
				}
				defer tc.Close()

				req, err := http.ReadRequest(bufio.NewReader(tc))
				if err != nil {
					return
				}
				body := fmt.Sprintf("upgraded:%s", req.URL.Path)
				fmt.Fprintf(tc, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
					len(body), body)
			}(raw)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return &startTLSServer{
		Host:  "localhost",
		Port:  strconv.Itoa(addr.Port),
		CAPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		close: func() { ln.Close() },
	}
}

// readLine reads one byte at a time so nothing beyond the newline is consumed.
func readLine(c net.Conn) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for b.Len() < 64 {
		n, err := c.Read(buf)
		if err != nil {
			return "", err
		}
		if n == 0 {
			continue
		}
		if buf[0] == '\n' {
			return b.String(), nil
		}
		b.WriteByte(buf[0])
	}
	return "", errors.New("line too long")
}

// startTLS runs the plaintext phase and then upgrades the same connection.
// This is the sequence a PostgreSQL or MySQL driver performs, written only
// against the exported API so it compiles on both compilers.
func startTLS(t *testing.T, srv *startTLSServer, cfg *Config) (net.Conn, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := DialPlain(ctx, srv.Host, srv.Port)
	if err != nil {
		t.Fatalf("DialPlain: %v", err)
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		conn.Close()
		t.Fatalf("SetDeadline: %v", err)
	}

	if _, err := conn.Write([]byte("STARTTLS\n")); err != nil {
		conn.Close()
		t.Fatalf("plaintext write: %v", err)
	}
	reply := make([]byte, 3)
	if _, err := io.ReadFull(conn, reply); err != nil {
		conn.Close()
		t.Fatalf("plaintext read: %v", err)
	}
	if string(reply) != "OK\n" {
		conn.Close()
		t.Fatalf("server reply = %q, want %q", reply, "OK\n")
	}

	// Clear the plaintext deadline; the handshake is bounded by ctx.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		t.Fatalf("clear deadline: %v", err)
	}

	tlsConn, err := Upgrade(ctx, conn, srv.Host, cfg)
	if err != nil {
		// Upgrade leaves the connection to the caller on failure.
		conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// TestUpgradeRejectsPlainConn pins the contract that a connection with no
// descriptor cannot be upgraded on the native path, rather than failing later
// in some confusing way.
func TestUpgradeRejectsPlainConn(t *testing.T) {
	srv := newStartTLSServer(t)
	defer srv.close()

	raw, err := net.Dial("tcp", net.JoinHostPort(srv.Host, srv.Port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer raw.Close()

	conn, err := Upgrade(context.Background(), raw, srv.Host, NewConfig(WithInsecureSkipVerify(true)))
	if err == nil {
		conn.Close()
		// std go accepts any net.Conn, so this only has to hold natively.
		if backendName != "crypto/tls" {
			t.Fatal("expected ErrNotUpgradable for a conn with no descriptor")
		}
		return
	}
	if backendName != "crypto/tls" && !errors.Is(err, ErrNotUpgradable) {
		t.Fatalf("error = %v, want ErrNotUpgradable", err)
	}
}

// TestUpgradeTLS is the reason the darwin build carries Secure Transport at
// all: Network.framework cannot start TLS on a socket that already carried
// plaintext, and this is that case.
func TestUpgradeTLS(t *testing.T) {
	srv := newStartTLSServer(t)
	defer srv.close()

	conn, err := startTLS(t, srv, NewConfig(WithRootCAPEM(srv.CAPEM)))
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "GET /hello HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", srv.Host)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response over upgraded conn: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "upgraded:/hello" {
		t.Fatalf("body = %q, want %q", body, "upgraded:/hello")
	}
}

// The upgrade path must verify exactly like the dial path does.
func TestUpgradeTLSRejectsUntrustedRoot(t *testing.T) {
	srv := newStartTLSServer(t)
	defer srv.close()

	conn, err := startTLS(t, srv, NewConfig())
	if err == nil {
		conn.Close()
		t.Fatal("expected rejection: the server certificate is self-signed")
	}
	if !errors.Is(err, ErrUntrustedRoot) && !errors.Is(err, ErrCertificateInvalid) {
		t.Fatalf("error = %v, want a certificate error", err)
	}
}

func TestUpgradeTLSSkipVerify(t *testing.T) {
	srv := newStartTLSServer(t)
	defer srv.close()

	conn, err := startTLS(t, srv, NewConfig(WithInsecureSkipVerify(true)))
	if err != nil {
		t.Fatalf("upgrade with skip-verify: %v", err)
	}
	conn.Close()
}
