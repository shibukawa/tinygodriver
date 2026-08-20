//go:build (tinygo || force_tinygo_logic) && (wasip2 || (!darwin && !linux && !windows))

package https

import (
	"context"
	"net"
	"time"
)

// Platforms with no native backend have nothing to tunnel through, so these
// pass the request straight to the dialer that reports
// ErrPlatformNotSupported. Keeping the shape identical means
// roundtrip_native.go needs no build tags of its own.

func dialTLSMaybeProxy(ctx context.Context, host, port string, cfg *Config, timeout time.Duration, p *proxy) (net.Conn, error) {
	return dialTLS(ctx, host, port, cfg, timeout)
}

func dialPlainMaybeProxy(ctx context.Context, host, port string, timeout time.Duration, p *proxy) (net.Conn, error) {
	return net.DialTimeout("tcp4", net.JoinHostPort(host, port), timeout)
}
