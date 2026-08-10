//go:build tinygo || force_tinygo_logic

package cloudhttp

import (
	"net/http"

	"github.com/shibukawa/tinygodriver/https"
)

// Backend identifies the selected HTTP stack.
const Backend = "https"

func newTransport(opts ClientOptions) http.RoundTripper {
	tr := https.NewTransport()
	tr.MaxIdleConnsPerHost = opts.MaxIdleConnsPerHost
	return tr
}
