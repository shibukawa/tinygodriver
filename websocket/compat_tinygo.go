//go:build tinygo || force_tinygo_logic

// The TinyGo half of the fork's divergences. TinyGo ships crypto/tls as a stub
// that does not handshake: as of 0.42 tls.Client is literally
// `return &Conn{Conn: conn}`, so it hands back the plaintext connection and
// Handshake reports no error. See PATCHES.md.

package websocket

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "tinygo"

// ErrTLSUnsupported reports that this build cannot originate a TLS handshake
// in-process. The handshake belongs to the OS or the device, so a wss:// URL
// needs a dialer that returns a connection already through it:
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

// clientTLS refuses. Reaching it means the caller asked for wss:// without
// supplying NetDialTLSContext; see ErrTLSUnsupported.
//
// Refusing is the whole point of the patch. Handing netConn to TinyGo's
// tls.Client would return it unchanged and report a successful handshake, so
// the request would go out in the clear with nothing to indicate it.
//
// netConn is returned unchanged so that DialContext's deferred close still has
// the connection to close, matching what the standard build does on a failed
// handshake.
func clientTLS(ctx context.Context, netConn net.Conn, cfg *tls.Config) (net.Conn, tls.ConnectionState, error) {
	return netConn, tls.ConnectionState{}, ErrTLSUnsupported
}
