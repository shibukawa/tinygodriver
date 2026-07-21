//go:build darwin || (linux && !tinygo)

package netdev

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIPProtoTLS(t *testing.T) {
	certPEM, keyPEM := testCertificate(t)
	certFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", certFile)

	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, 4)
		if _, err := conn.Read(buf); err != nil {
			serverErr <- err
			return
		}
		if string(buf) != "PING" {
			serverErr <- &unexpectedPayloadError{got: string(buf)}
			return
		}
		_, err = conn.Write([]byte("PONG"))
		serverErr <- err
	}()

	d := New()
	fd, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TLS)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close(fd)
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	if err := d.Connect(fd, "localhost", netip.AddrPortFrom(netip.Addr{}, port)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	if n, err := d.Send(fd, []byte("PING"), 0, deadline); err != nil || n != 4 {
		t.Fatalf("Send() = %d, %v", n, err)
	}
	buf := make([]byte, 4)
	if n, err := d.Recv(fd, buf, 0, deadline); err != nil || n != 4 {
		t.Fatalf("Recv() = %d, %v", n, err)
	}
	if string(buf) != "PONG" {
		t.Fatalf("Recv() payload = %q", buf)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

type unexpectedPayloadError struct {
	got string
}

func (e *unexpectedPayloadError) Error() string {
	return "unexpected TLS test payload: " + e.got
}

func testCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
