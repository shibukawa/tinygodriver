//go:build force_tinygo_logic && !tinygo && windows

package https_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
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
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/https"
)

// The Schannel backend needs its own mutual TLS test because it is the one
// backend with a key type restriction: Schannel wants a CERT_CONTEXT carrying a
// CNG key handle, and the only blob CryptDecodeObjectEx hands straight to
// NCryptImportKey is RSA. TestMutualTLS in clientcert_std_test.go uses an EC
// key, which is exactly what this backend refuses.

type mtlsFixture struct {
	url           string
	caPEM         []byte
	clientCertPEM []byte
	caKey         *ecdsa.PrivateKey
	caCert        *x509.Certificate
	stop          func()
}

func newMTLSFixture(t *testing.T) *mtlsFixture {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:              []string{"localhost"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	mux := http.NewServeMux()
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "no client cert", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, r.TLS.PeerCertificates[0].Subject.CommonName)
	})

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{caDER}, PrivateKey: caKey}},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    pool,
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"http/1.1"},
		},
	}
	go srv.ServeTLS(ln, "", "")

	return &mtlsFixture{
		url:    fmt.Sprintf("https://localhost:%d/whoami", ln.Addr().(*net.TCPAddr).Port),
		caPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		caKey:  caKey,
		caCert: caCert,
		stop:   func() { srv.Close() },
	}
}

// issueClient signs a client certificate for pub and returns it PEM encoded.
func (f *mtlsFixture) issueClient(t *testing.T, pub any) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, f.caCert, pub, f.caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// TestMutualTLSRSA covers both PEM encodings of an RSA key that a user is
// likely to have on disk: the bare PKCS#1 form and the PKCS#8 wrapper the C
// layer has to unwrap first.
func TestMutualTLSRSA(t *testing.T) {
	f := newMTLSFixture(t)
	defer f.stop()

	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := f.issueClient(t, &clientKey.PublicKey)

	pkcs8DER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		keyPEM []byte
	}{
		{"PKCS1", pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey),
		})},
		{"PKCS8", pem.EncodeToMemory(&pem.Block{
			Type: "PRIVATE KEY", Bytes: pkcs8DER,
		})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := https.NewClient(
				https.WithRootCAPEM(f.caPEM),
				https.WithClientCertificate(certPEM, tc.keyPEM),
			)
			resp, err := client.Get(f.url)
			if err != nil {
				t.Fatalf("mTLS get: %v", err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			if string(body) != "client" {
				t.Fatalf("body = %q, want %q", body, "client")
			}
		})
	}
}

// TestMutualTLSRequiresCertificate pins that the server really is demanding a
// client certificate, so the success above is not a false positive.
func TestMutualTLSRequiresCertificate(t *testing.T) {
	f := newMTLSFixture(t)
	defer f.stop()

	client := https.NewClient(https.WithRootCAPEM(f.caPEM))
	if resp, err := client.Get(f.url); err == nil {
		resp.Body.Close()
		t.Fatal("expected rejection without a client certificate")
	}
}

// TestClientCertificateECRejected pins the documented Schannel limitation. An
// EC key must be refused rather than silently ignored, which would send no
// certificate at all and fail much later with a confusing error.
func TestClientCertificateECRejected(t *testing.T) {
	f := newMTLSFixture(t)
	defer f.stop()

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := f.issueClient(t, &clientKey.PublicKey)
	keyDER, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	client := https.NewClient(
		https.WithRootCAPEM(f.caPEM),
		https.WithClientCertificate(certPEM, keyPEM),
	)
	resp, err := client.Get(f.url)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected ErrClientCertificateUnsupported")
	}
	if !errors.Is(err, https.ErrClientCertificateUnsupported) {
		t.Fatalf("error = %v, want ErrClientCertificateUnsupported", err)
	}
}
