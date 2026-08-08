// Definitions shared by both halves of the fork's divergences. See PATCHES.md
// for what diverges and why; compat_std.go and compat_tinygo.go hold the two
// implementations.

package fasthttp

import "net"

// tlsClientConn is what tlsClient returns: a connection that still needs its
// handshake driven. Upstream used *tls.Conn directly, which is not a type on
// TinyGo, so the seam is an interface. tlsConn, declared in server.go, is the
// server-side counterpart and stays upstream's.
type tlsClientConn interface {
	net.Conn
	Handshake() error
}
