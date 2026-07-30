package https

import (
	"net/http"
	"time"
)

// Transport is an http.RoundTripper that speaks HTTPS through the host OS TLS
// stack. In standard Go builds it delegates to net/http.
//
// Transport is safe for concurrent use.
type Transport struct {
	// Config holds the TLS settings. Nil means the secure defaults.
	Config *Config

	// DialTimeout bounds connection setup and the TLS handshake.
	// Zero means 30 seconds.
	DialTimeout time.Duration

	// ResponseTimeout bounds reading the response headers and body.
	// Zero means no limit beyond the request context.
	ResponseTimeout time.Duration

	// MaxIdleConnsPerHost is how many idle connections are kept per
	// destination for reuse. Zero means 2.
	MaxIdleConnsPerHost int

	// IdleConnTimeout is how long a connection may sit idle and still be
	// reused. Zero means 20 seconds.
	//
	// The native path cannot detect that a peer closed an idle connection, so
	// this bounds how long a connection is assumed live rather than merely
	// releasing memory. Keep it below the server's own idle timeout.
	IdleConnTimeout time.Duration

	// DisableKeepAlives closes every connection after a single request,
	// which is what this package did before connection reuse existed.
	DisableKeepAlives bool

	std  stdTransport // zero value is usable; only populated in std Go builds
	pool connPool     // zero value is usable; only populated on the native path
}

var _ http.RoundTripper = (*Transport)(nil)

// DefaultTransport is used by DefaultClient and the package-level helpers.
var DefaultTransport = &Transport{}

// NewTransport builds a Transport from Config options.
func NewTransport(opts ...Option) *Transport {
	return &Transport{Config: NewConfig(opts...)}
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil {
		req.Body.Close()
		return nil, &Error{Op: "dial", Backend: backendName, Err: ErrHandshakeFailed}
	}
	if cfg := t.Config; cfg != nil && cfg.err != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, cfg.err
	}
	return t.roundTrip(req)
}

func (t *Transport) dialTimeout() time.Duration {
	if t.DialTimeout <= 0 {
		return 30 * time.Second
	}
	return t.DialTimeout
}
