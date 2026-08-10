package aws

import (
	"net/http"

	"github.com/shibukawa/tinygodriver/internal/cloudhttp"
)

// ClientOptions configures the HTTP client a service package uses when the
// caller supplies none.
type ClientOptions = cloudhttp.ClientOptions

// NewHTTPClient builds the default HTTP client for a service package. The
// idle-connection setting is forwarded to https.Transport on TinyGo builds and
// to net/http.Transport otherwise, so it means the same thing on both paths.
func NewHTTPClient(opts ClientOptions) *http.Client {
	return cloudhttp.NewClient(opts)
}

// CloseIdleConnections releases the pooled connections of client, if its
// transport keeps any. Both https.Transport and net/http.Transport do.
//
// It exists because a service client should be closable without knowing which
// transport it was given, including one the caller supplied.
func CloseIdleConnections(client *http.Client) {
	cloudhttp.CloseIdleConnections(client)
}
