package cloudhttp

import (
	"context"
	"net/http"
	"time"
)

// ClientOptions configures a service-owned HTTP client.
type ClientOptions struct {
	Timeout             time.Duration
	MaxIdleConnsPerHost int
}

// NewClient builds the HTTP client used when a service package owns it.
func NewClient(opts ClientOptions) *http.Client {
	return &http.Client{
		Transport: newTransport(opts),
		Timeout:   opts.Timeout,
	}
}

// OperationContext applies one timeout budget to a complete logical service
// operation. An earlier caller deadline still wins through context.WithTimeout.
func OperationContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// CloseIdleConnections releases pooled connections without depending on the
// concrete transport selected by the build.
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
