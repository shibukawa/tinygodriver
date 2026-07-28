//go:build !tinygo && !force_tinygo_logic

package s3

import (
	"net/http"
	"time"
)

// Backend identifies the HTTP stack selected by build constraints.
const Backend = "net/http"

// newHTTPClient returns the client used when none is supplied.
func newHTTPClient(timeout time.Duration) *http.Client {
	client := &http.Client{Timeout: timeout}
	applyRedirectPolicy(client)
	return client
}
