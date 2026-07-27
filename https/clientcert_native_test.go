//go:build force_tinygo_logic && !tinygo && darwin

package https_test

import (
	"errors"
	"testing"

	"github.com/shibukawa/tinygodriver/https"
)

// TestClientCertificateUnsupported pins the documented darwin limitation:
// Network.framework needs a SecIdentityRef, which requires importing the key
// into a keychain, so a client certificate is refused rather than silently
// ignored.
func TestClientCertificateUnsupported(t *testing.T) {
	client := https.NewClient(
		https.WithInsecureSkipVerify(true),
		https.WithClientCertificate([]byte("cert"), []byte("key")),
	)
	resp, err := client.Get("https://localhost:1/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected ErrClientCertificateUnsupported")
	}
	if !errors.Is(err, https.ErrClientCertificateUnsupported) {
		t.Fatalf("error = %v, want ErrClientCertificateUnsupported", err)
	}
}
