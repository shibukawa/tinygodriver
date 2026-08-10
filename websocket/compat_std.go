//go:build !tinygo && !force_tinygo_logic

// The standard-Go half of the fork's divergences. Everything here is upstream
// behaviour, expressed through the seams the TinyGo half needs. See PATCHES.md.

package websocket

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "std"

// ErrTLSUnsupported is never returned by this build: standard Go performs the
// handshake in process. It is declared here so the package presents the same
// API under every build tag, and so a caller can compare against it without
// build tags of its own.
//
// The TinyGo build returns it from any wss:// dial that supplies no
// Dialer.NetDialTLSContext; see compat_tinygo.go for what to set.
var ErrTLSUnsupported = errors.New("websocket: in-process TLS is unavailable on TinyGo; set Dialer.NetDialTLSContext")

// proxyFromEnvironment is http.ProxyFromEnvironment. TinyGo's net/http defines
// neither it nor a real Transport, so DefaultDialer cannot name it directly.
func proxyFromEnvironment(req *http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment(req)
}

// clientTLS wraps netConn in a client-side TLS connection and completes the
// handshake, returning the connection and the negotiated state. It replaces
// upstream's tls.Client plus doHandshake, which named three symbols TinyGo does
// not have.
//
// The returned connection is meaningful even when the error is not nil:
// upstream assigned tls.Client's result to netConn before handshaking, so that
// the deferred close in DialContext still had something to close.
func clientTLS(ctx context.Context, netConn net.Conn, cfg *tls.Config) (net.Conn, tls.ConnectionState, error) {
	tlsConn := tls.Client(netConn, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return tlsConn, tls.ConnectionState{}, err
	}
	if !cfg.InsecureSkipVerify {
		if err := tlsConn.VerifyHostname(cfg.ServerName); err != nil {
			return tlsConn, tlsConn.ConnectionState(), err
		}
	}
	return tlsConn, tlsConn.ConnectionState(), nil
}

// cloneTLSConfig is upstream's own, moved here because TinyGo's tls.Config has
// no Clone method.
func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{}
	}
	return cfg.Clone()
}
