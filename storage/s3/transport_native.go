//go:build tinygo || force_tinygo_logic

package s3

import (
	"net/http"
	"time"

	"github.com/shibukawa/tinygodriver/https"
)

// Backend identifies the HTTP stack selected by build constraints.
const Backend = "https"

// newHTTPClient returns a client whose transport reaches https URLs through the
// TLS stack of the host OS, because TinyGo's crypto/tls is a stub. The same
// transport carries plain http, which is what S3-compatible servers on a local
// network usually speak.
func newHTTPClient(timeout time.Duration) *http.Client {
	client := &http.Client{
		Transport: https.NewTransport(),
		Timeout:   timeout,
	}
	applyRedirectPolicy(client)
	return client
}
