//go:build tinygo

// The TinyGo half of the fork's divergences. TinyGo ships crypto/tls as a stub
// whose Client panics and whose Config, ConnectionState and Conn are missing
// most of their shape, and its net package defines no Resolver. See PATCHES.md.
//
// force_tinygo_logic deliberately does not select this file; see compat_std.go
// for why.

package fasthttp

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "tinygo"

// resolveInDialer says whether name resolution is the dialer's job. On TinyGo it
// is: netdev resolves inside Connect, and there is no Resolver to call instead.
// TCPDialer's own DNS cache is therefore unused, and so is its resolver.
const resolveInDialer = true

// tlsConnImpl is TinyGo's concrete TLS connection type. crypto/tls has no Conn
// there because the handshake belongs to the device; net.TLSConn is what a
// successful net.DialTLS returns, and it carries the Handshake method that
// perIPTLSConn and the tlsConn interface both need.
type tlsConnImpl = net.TLSConn

// ErrTLSUnsupported reports that this build cannot perform a TLS handshake.
// Terminating TLS needs tls.Server and tls.X509KeyPair, which TinyGo does not
// define, and originating it needs tls.Client, which panics.
//
// A client can still speak HTTPS by supplying a Dial that returns an
// already-encrypted connection, because dialAddr treats any connection with a
// Handshake method as one that has been through TLS already:
//
//	hc := &fasthttp.HostClient{
//		Addr:  "example.com:443",
//		IsTLS: true,
//		Dial:  func(addr string) (net.Conn, error) { return net.DialTLS(addr) },
//	}
//
// A server has no equivalent escape hatch: put a TLS terminator in front.
var ErrTLSUnsupported = errors.New("fasthttp: TLS is not supported on TinyGo")

// cloneTLSConfig stands in for (*tls.Config).Clone, which TinyGo's stub does not
// define. It reproduces standard Go's semantics exactly -- nil for nil, and a
// shallow copy whose slices stay shared with the original -- so that code
// depending on either behaviour reads the same on both compilers.
//
// The fields are listed rather than copied with clone := *c because Config
// carries a sync.RWMutex, and a struct assignment would copy the lock. This is
// the whole exported surface of TinyGo's Config; a version bump that adds a
// field must add it here too.
func cloneTLSConfig(c *tls.Config) *tls.Config {
	if c == nil {
		return nil
	}
	return &tls.Config{
		Rand:                        c.Rand,
		Time:                        c.Time,
		Certificates:                c.Certificates,
		NameToCertificate:           c.NameToCertificate,
		GetCertificate:              c.GetCertificate,
		GetClientCertificate:        c.GetClientCertificate,
		GetConfigForClient:          c.GetConfigForClient,
		VerifyPeerCertificate:       c.VerifyPeerCertificate,
		VerifyConnection:            c.VerifyConnection,
		RootCAs:                     c.RootCAs,
		NextProtos:                  c.NextProtos,
		ServerName:                  c.ServerName,
		ClientAuth:                  c.ClientAuth,
		ClientCAs:                   c.ClientCAs,
		InsecureSkipVerify:          c.InsecureSkipVerify,
		CipherSuites:                c.CipherSuites,
		PreferServerCipherSuites:    c.PreferServerCipherSuites,
		SessionTicketsDisabled:      c.SessionTicketsDisabled,
		SessionTicketKey:            c.SessionTicketKey,
		ClientSessionCache:          c.ClientSessionCache,
		UnwrapSession:               c.UnwrapSession,
		WrapSession:                 c.WrapSession,
		MinVersion:                  c.MinVersion,
		MaxVersion:                  c.MaxVersion,
		CurvePreferences:            c.CurvePreferences,
		DynamicRecordSizingDisabled: c.DynamicRecordSizingDisabled,
		Renegotiation:               c.Renegotiation,
		KeyLogWriter:                c.KeyLogWriter,
	}
}

// negotiatedProtocol always reports no protocol: TinyGo's ConnectionState has no
// NegotiatedProtocol field, so ALPN cannot be observed and the handlers
// registered with Server.NextProto -- HTTP/2 among them -- never run.
func negotiatedProtocol(tc tlsConn) string { return "" }

func tlsClient(conn net.Conn, cfg *tls.Config) (tlsClientConn, error) {
	return nil, ErrTLSUnsupported
}

func newTLSListener(ln net.Listener, cfg *tls.Config) (net.Listener, error) {
	return nil, ErrTLSUnsupported
}

func x509KeyPair(certData, keyData []byte) (tls.Certificate, error) {
	if len(certData) == 0 && len(keyData) == 0 {
		return tls.Certificate{}, errNoCertOrKeyProvided
	}
	return tls.Certificate{}, ErrTLSUnsupported
}

// deferredResolver is what an explicit nil Resolver resolves to. Reaching it
// means resolveInDialer was bypassed, which only a caller that set
// DisableDNSResolution false and Resolver nil on purpose can arrange.
type deferredResolver struct{}

func (deferredResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	return nil, errors.New("fasthttp: TinyGo resolves names in the dialer; set TCPDialer.Resolver to override")
}

func defaultResolver() Resolver { return deferredResolver{} }

// copyZeroAlloc drops upstream's sendfile fast paths: TinyGo's os.File and
// net.TCPConn implement neither ReadFrom nor WriteTo, so there is nothing to
// hand the kernel and the buffered copy is the only path.
func copyZeroAlloc(w io.Writer, r io.Reader) (int64, error) {
	if rt, ok := r.(io.WriterTo); ok {
		return rt.WriteTo(w)
	}
	if wt, ok := w.(io.ReaderFrom); ok {
		return wt.ReadFrom(r)
	}

	vbuf := copyBufPool.Get()
	buf := vbuf.([]byte) //nolint:forcetypeassert
	n, err := copyBuffer(w, r, buf)
	copyBufPool.Put(vbuf)
	return n, err
}
