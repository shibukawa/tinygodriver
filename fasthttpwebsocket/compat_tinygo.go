//go:build tinygo

// The TinyGo half of the fork's divergences. TinyGo ships crypto/tls as a stub
// whose Client panics and whose Conn type does not exist, and its net/http
// Transport is an empty struct, so ProxyFromEnvironment is absent too. See
// PATCHES.md.
//
// force_tinygo_logic deliberately does not select this file; see compat_std.go
// for why.

package websocket

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"

	"golang.org/x/net/http/httpproxy"
)

// ErrTLSUnsupported reports that this build cannot originate a TLS handshake.
// TinyGo's tls.Client compiles and then panics, which is worse than a stub that
// fails, because nothing warns you until a dial is in flight.
//
// wss:// still works, through the hook upstream already provides: give the
// dialer a NetDialTLSContext that hands back a connection the OS has already
// encrypted.
//
//	d := &websocket.Dialer{
//		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
//			return net.DialTLS(addr)
//		},
//	}
//
// A server has no equivalent: terminating TLS needs tls.Server and
// tls.X509KeyPair, neither of which TinyGo defines. Put a terminator in front.
var ErrTLSUnsupported = errors.New("websocket: originating TLS is not supported on TinyGo; set Dialer.NetDialTLSContext")

// envProxyFunc mirrors net/http's own unexported one. TinyGo's http.Transport
// is an empty struct and ProxyFromEnvironment does not exist, but the package
// standard Go delegates to is ordinary Go and builds here, so the environment
// is read by exactly the same code with exactly the same precedence.
var envProxyFunc = sync.OnceValue(func() func(*url.URL) (*url.URL, error) {
	return httpproxy.FromEnvironment().ProxyFunc()
})

// defaultProxy is what DefaultDialer.Proxy is set to.
func defaultProxy(req *http.Request) (*url.URL, error) {
	return envProxyFunc()(req.URL)
}

// cloneTLSConfig stands in for (*tls.Config).Clone, which TinyGo's stub does not
// define. It keeps upstream's semantics -- an empty config for nil, and a
// shallow copy whose slices stay shared with the original.
//
// The fields are listed rather than copied with clone := *cfg because Config
// carries a sync.RWMutex, and a struct assignment would copy the lock. This is
// the whole exported surface of TinyGo's Config; a version bump that adds a
// field must add it here too.
func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{}
	}
	return &tls.Config{
		Rand:                        cfg.Rand,
		Time:                        cfg.Time,
		Certificates:                cfg.Certificates,
		NameToCertificate:           cfg.NameToCertificate,
		GetCertificate:              cfg.GetCertificate,
		GetClientCertificate:        cfg.GetClientCertificate,
		GetConfigForClient:          cfg.GetConfigForClient,
		VerifyPeerCertificate:       cfg.VerifyPeerCertificate,
		VerifyConnection:            cfg.VerifyConnection,
		RootCAs:                     cfg.RootCAs,
		NextProtos:                  cfg.NextProtos,
		ServerName:                  cfg.ServerName,
		ClientAuth:                  cfg.ClientAuth,
		ClientCAs:                   cfg.ClientCAs,
		InsecureSkipVerify:          cfg.InsecureSkipVerify,
		CipherSuites:                cfg.CipherSuites,
		PreferServerCipherSuites:    cfg.PreferServerCipherSuites,
		SessionTicketsDisabled:      cfg.SessionTicketsDisabled,
		SessionTicketKey:            cfg.SessionTicketKey,
		ClientSessionCache:          cfg.ClientSessionCache,
		UnwrapSession:               cfg.UnwrapSession,
		WrapSession:                 cfg.WrapSession,
		MinVersion:                  cfg.MinVersion,
		MaxVersion:                  cfg.MaxVersion,
		CurvePreferences:            cfg.CurvePreferences,
		DynamicRecordSizingDisabled: cfg.DynamicRecordSizingDisabled,
		Renegotiation:               cfg.Renegotiation,
		KeyLogWriter:                cfg.KeyLogWriter,
	}
}

// tlsClientHandshake refuses rather than handshaking. netConn comes back
// unwrapped so the caller's deferred Close still reaches the socket.
//
// The trace callbacks do not fire: no handshake is begun, so there is no start
// to report and no ConnectionState to report at the end.
func tlsClientHandshake(_ context.Context, netConn net.Conn, _ *tls.Config, _ *httptrace.ClientTrace) (net.Conn, error) {
	return netConn, ErrTLSUnsupported
}
