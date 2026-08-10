//go:build !tinygo && !force_tinygo_logic

package cloudhttp

import "net/http"

// Backend identifies the selected HTTP stack.
const Backend = "net/http"

func newTransport(opts ClientOptions) http.RoundTripper {
	if opts.MaxIdleConnsPerHost <= 0 {
		return nil
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = opts.MaxIdleConnsPerHost
	return tr
}
