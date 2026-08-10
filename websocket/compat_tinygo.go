//go:build tinygo || force_tinygo_logic

// The TinyGo half of the fork's divergences. TinyGo ships crypto/tls as a stub
// whose Client panics and whose Config and Conn are missing most of their
// shape, and its net/http has no Transport worth the name. See PATCHES.md.

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
const Backend = "tinygo"

// ErrTLSUnsupported reports that this build cannot originate a TLS handshake
// in-process. TinyGo's crypto/tls.Client panics, because the handshake belongs
// to the OS or the device, so a wss:// URL needs a dialer that returns a
// connection already through the handshake:
//
//	d := websocket.Dialer{
//		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
//			return net.DialTLS(addr)
//		},
//	}
//	c, resp, err := d.Dial("wss://example.com/ws", nil)
//
// On darwin that handshake runs through netdev's Secure Transport backend,
// which takes extra trust anchors from SSL_CERT_FILE.
//
// A server has no equivalent escape hatch: TinyGo defines neither tls.Server
// nor X509KeyPair, so terminate TLS in front of the process.
var ErrTLSUnsupported = errors.New("websocket: in-process TLS is unavailable on TinyGo; set Dialer.NetDialTLSContext")

// proxyFromEnvironment reports no proxy. TinyGo's net/http defines neither
// ProxyFromEnvironment nor a Transport to read one for, and a device has no
// environment to read it from. A caller that needs a proxy sets Dialer.Proxy.
func proxyFromEnvironment(req *http.Request) (*url.URL, error) {
	return nil, nil
}

// clientTLS refuses rather than panicking. Reaching it means the caller asked
// for wss:// without supplying NetDialTLSContext; see ErrTLSUnsupported.
//
// netConn is returned unchanged so that DialContext's deferred close still has
// the connection to close, matching what the standard build does on a failed
// handshake.
func clientTLS(ctx context.Context, netConn net.Conn, cfg *tls.Config) (net.Conn, tls.ConnectionState, error) {
	return netConn, tls.ConnectionState{}, ErrTLSUnsupported
}

// cloneTLSConfig stands in for (*tls.Config).Clone, which TinyGo's stub does
// not define. It reproduces standard Go's semantics -- a shallow copy whose
// slices stay shared with the original -- except that upstream's nil case
// returns an empty Config rather than nil, which is what the caller here wants.
//
// The fields are listed rather than copied with clone := *cfg because Config
// carries a sync.RWMutex and a struct assignment would copy the lock. This is
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
		MinVersion:                  cfg.MinVersion,
		MaxVersion:                  cfg.MaxVersion,
		CurvePreferences:            cfg.CurvePreferences,
		DynamicRecordSizingDisabled: cfg.DynamicRecordSizingDisabled,
		Renegotiation:               cfg.Renegotiation,
		KeyLogWriter:                cfg.KeyLogWriter,
	}
}
