//go:build (tinygo || force_tinygo_logic) && !darwin && !linux

package https

import (
	"context"
	"net"
	"time"
)

const backendName = "unsupported"

// dialTLS reports that no native backend exists for this platform yet.
// It never falls back to an unverified or plaintext connection.
func dialTLS(ctx context.Context, host, port string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	return nil, &Error{
		Op:      "dial",
		Host:    host,
		Backend: backendName,
		Err:     ErrPlatformNotSupported,
	}
}
