package aws

import (
	"net/http"
	"time"
)

// ClientOptions configures the HTTP client a service package uses when the
// caller supplies none.
type ClientOptions struct {
	// Timeout bounds one request, including reading the response body.
	Timeout time.Duration

	// MaxIdleConnsPerHost is how many idle connections are kept per
	// destination. Zero takes the transport's own default, which is 2 on the
	// native path.
	//
	// A client that talks to one endpoint should set this to the concurrency it
	// runs: with every request going to the same host, the per-host cap is the
	// whole pool.
	MaxIdleConnsPerHost int
}

// NewHTTPClient builds the default HTTP client for a service package. The
// idle-connection setting is forwarded to https.Transport on TinyGo builds and
// to net/http.Transport otherwise, so it means the same thing on both paths.
func NewHTTPClient(opts ClientOptions) *http.Client {
	return &http.Client{
		Transport: newTransport(opts),
		Timeout:   opts.Timeout,
	}
}

// CloseIdleConnections releases the pooled connections of client, if its
// transport keeps any. Both https.Transport and net/http.Transport do.
//
// It exists because a service client should be closable without knowing which
// transport it was given, including one the caller supplied.
func CloseIdleConnections(client *http.Client) {
	type idleCloser interface{ CloseIdleConnections() }
	if client == nil {
		return
	}
	if tr, ok := client.Transport.(idleCloser); ok {
		tr.CloseIdleConnections()
		return
	}
	if client.Transport == nil {
		if tr, ok := http.DefaultTransport.(idleCloser); ok {
			tr.CloseIdleConnections()
		}
	}
}
