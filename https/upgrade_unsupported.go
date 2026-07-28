//go:build (tinygo || force_tinygo_logic) && !darwin && !linux

package https

import (
	"context"
	"net"
)

// DialPlain and Upgrade exist on every build so portable code compiles
// everywhere, but this platform has no native backend. Neither ever falls back
// to an unverified or plaintext connection.

func DialPlain(ctx context.Context, host, port string) (net.Conn, error) {
	return nil, &Error{
		Op:      "dial",
		Host:    host,
		Backend: backendName,
		Err:     ErrPlatformNotSupported,
	}
}

func Upgrade(ctx context.Context, conn net.Conn, host string, cfg *Config) (net.Conn, error) {
	return nil, &Error{
		Op:      "upgrade",
		Host:    host,
		Backend: backendName,
		Err:     ErrPlatformNotSupported,
	}
}
