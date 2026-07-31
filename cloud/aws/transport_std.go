//go:build !tinygo && !force_tinygo_logic

package aws

import "net/http"

// Backend identifies the HTTP stack selected by build constraints.
const Backend = "net/http"

// newTransport returns a net/http transport carrying the same idle-connection
// setting as the native one, so pool behavior does not depend on the build tag.
func newTransport(opts ClientOptions) http.RoundTripper {
	if opts.MaxIdleConnsPerHost <= 0 {
		return nil // http.DefaultTransport
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = opts.MaxIdleConnsPerHost
	return tr
}
