package pgconn

// Local patch: see ../../PATCHES.md
//
// TinyGo ships crypto/tls as a stub, so this build cannot use it. TLS instead
// goes through the https package, which starts TLS on an already-connected
// socket using the platform's native stack. That is exactly the shape
// PostgreSQL needs, because SSLRequest is negotiated in plaintext first.

import (
	"context"
	"errors"
	"net"

	"github.com/shibukawa/tinygodriver/https"
)

// ErrTLSUnsupported reports that TLS could not be started. Refusing is the only
// safe answer: falling back to plaintext would hand an unencrypted connection
// to a caller who asked for an encrypted one.
var ErrTLSUnsupported = errors.New("pgconn: TLS is not supported on this platform")

// TLSConfig stands in for *tls.Config so Config keeps its shape without linking
// crypto/tls. It carries what the https backends can act on.
//
// Client certificates are absent because the native backends cannot offer one;
// configTLS rejects sslcert and sslkey rather than ignoring them.
type TLSConfig struct {
	// Host is the name to verify the certificate against.
	Host string

	// ServerName overrides Host for SNI. Empty when the connection targets a
	// literal IP, per RFC 6066.
	ServerName string

	// RootCAsPEM are additional trust anchors read from sslrootcert.
	RootCAsPEM [][]byte

	// RootCAsOnly ignores the system trust store.
	RootCAsOnly bool

	// InsecureSkipVerify disables verification, for the sslmode values that
	// encrypt without authenticating.
	InsecureSkipVerify bool
}

// Clone matches the tls.Config method pgconn calls when copying fallbacks.
func (c *TLSConfig) Clone() *TLSConfig {
	if c == nil {
		return nil
	}
	d := *c
	return &d
}

// upgradeConn starts TLS on a connection that has already carried plaintext.
//
// The connection must come from https.DialPlain, which is what
// database/sql/pgxstdlib installs as Config.DialFunc: TinyGo cannot recover a
// descriptor from an arbitrary net.Conn, so the dialer has to supply one that
// carries its own.
func upgradeConn(ctx context.Context, conn net.Conn, cfg *TLSConfig) (net.Conn, error) {
	if cfg == nil {
		return nil, ErrTLSUnsupported
	}
	return https.Upgrade(ctx, conn, cfg.Host, &https.Config{
		RootCAs:            cfg.RootCAsPEM,
		RootCAsOnly:        cfg.RootCAsOnly,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         cfg.ServerName,
	})
}

// hostResolver defers name resolution to the dialer. TinyGo has no
// net.Resolver, and netdev resolves the host inside Connect anyway.
type hostResolver struct{}

func (hostResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return []string{host}, nil
}
