//go:build !tinygo && !force_tinygo_logic

package https

import (
	"context"
	"crypto/tls"
	"net"
)

// DialPlain opens a plaintext TCP connection suitable for Upgrade.
//
// On this path it is an ordinary net.Dial, so the returned connection is a
// *net.TCPConn with the standard library's deadline and cancellation behavior.
func DialPlain(ctx context.Context, host, port string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}
	return conn, nil
}

// Upgrade starts TLS on a connection that has already carried plaintext, and
// returns a net.Conn whose bytes are plaintext again.
//
// host is the SNI name and the name verified against the certificate;
// Config.ServerName overrides it. Verification completes before Upgrade
// returns, so a returned connection is a verified peer.
//
// On success the returned connection owns conn and closing it closes conn. On
// failure conn is left open and untouched, so a caller that wants to continue
// in plaintext still can.
//
// This path is crypto/tls, so it accepts any net.Conn.
func Upgrade(ctx context.Context, conn net.Conn, host string, cfg *Config) (net.Conn, error) {
	if conn == nil {
		return nil, &Error{Op: "upgrade", Host: host, Backend: backendName, Err: ErrNotUpgradable}
	}
	if cfg != nil && cfg.err != nil {
		return nil, cfg.err
	}

	tlsCfg, err := cfg.stdConfig(host)
	if err != nil {
		return nil, &Error{Op: "upgrade", Host: host, Backend: backendName, Err: err}
	}

	tc := tls.Client(conn, tlsCfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		return nil, upgradeStdError(host, err)
	}
	return tc, nil
}

// stdConfig converts the backend-neutral Config into a crypto/tls.Config for a
// known server name. Transport.tlsConfig does the same for the dial path; this
// one exists because Upgrade has no *Transport and must pin ServerName itself.
func (c *Config) stdConfig(host string) (*tls.Config, error) {
	t := &Transport{Config: c}
	out, err := t.tlsConfig()
	if err != nil {
		return nil, err
	}
	if out.ServerName == "" {
		out.ServerName = host
	}
	return out, nil
}

// upgradeStdError maps a handshake failure onto the same sentinels the native
// backends produce, so errors.Is behaves identically across compilers.
func upgradeStdError(host string, err error) error {
	mapped := classifyStdError("upgrade", host, err)
	if _, ok := mapped.(*Error); ok {
		return mapped
	}
	// Not a TLS failure. Wrap it so the caller still gets an *Error, since
	// unlike the RoundTrip path there is no net/http layer to preserve.
	return &Error{Op: "upgrade", Host: host, Backend: backendName, Err: mapped}
}
