//go:build tinygo || force_tinygo_logic

package aws

import (
	"net/http"

	"github.com/shibukawa/tinygodriver/https"
)

// Backend identifies the HTTP stack selected by build constraints.
const Backend = "https"

// newTransport returns a transport that reaches https URLs through the TLS
// stack of the host OS, because TinyGo's crypto/tls is a stub. The same
// transport carries plain http, which is what a service emulator on a local
// network usually speaks.
func newTransport(opts ClientOptions) http.RoundTripper {
	tr := https.NewTransport()
	tr.MaxIdleConnsPerHost = opts.MaxIdleConnsPerHost
	return tr
}
