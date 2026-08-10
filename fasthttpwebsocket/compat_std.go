//go:build !tinygo

// The standard-Go half of the fork's divergences: upstream's own code, moved
// here unchanged so the TinyGo half has somewhere to differ. See PATCHES.md.
//
// force_tinygo_logic deliberately does not select the TinyGo half. Every
// divergence in this pair is a standard-library symbol TinyGo does not define
// rather than alternate logic, so a host-Go build has nothing to exercise, and
// cloneTLSConfig would copy the mutex inside a real tls.Config.

package websocket

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
)

// defaultProxy is what DefaultDialer.Proxy is set to.
func defaultProxy(req *http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment(req)
}

// cloneTLSConfig is upstream's, verbatim. A nil config clones to an empty one
// rather than to nil, because the caller goes on to set ServerName on it.
func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{}
	}
	return cfg.Clone()
}

// tlsClientHandshake wraps netConn in a client TLS connection and completes the
// handshake. The returned connection is non-nil whenever the wrapper was
// created, error or not, so the caller's deferred Close still reaches it.
func tlsClientHandshake(ctx context.Context, netConn net.Conn, cfg *tls.Config, trace *httptrace.ClientTrace) (net.Conn, error) {
	tlsConn := tls.Client(netConn, cfg)

	if trace != nil && trace.TLSHandshakeStart != nil {
		trace.TLSHandshakeStart()
	}
	err := doHandshake(ctx, tlsConn, cfg)
	if trace != nil && trace.TLSHandshakeDone != nil {
		trace.TLSHandshakeDone(tlsConn.ConnectionState(), err)
	}

	return tlsConn, err
}

func doHandshake(ctx context.Context, tlsConn *tls.Conn, cfg *tls.Config) error {
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return err
	}
	if !cfg.InsecureSkipVerify {
		if err := tlsConn.VerifyHostname(cfg.ServerName); err != nil {
			return err
		}
	}
	return nil
}
