package https

import "net"

// In-band TLS upgrade, for protocols that negotiate in plaintext and then
// switch the same socket to TLS: PostgreSQL's SSLRequest, MySQL's capability
// exchange, SMTP and IMAP STARTTLS.
//
// The usual sequence is:
//
//	conn, err := https.DialPlain(ctx, "db.example.com", "5432")
//	// ... write the protocol's upgrade request, read its reply ...
//	tlsConn, err := https.Upgrade(ctx, conn, "db.example.com", cfg)
//
// Both calls compile under standard Go and TinyGo, so a driver written against
// them is shared between compilers. Under standard Go they are net.Dial and
// crypto/tls.Client; under TinyGo they are a netdev socket and the platform's
// native TLS backend.

// UpgradableConn is a plaintext connection that can be handed to Upgrade.
//
// TinyGo's net package does not implement SyscallConn, so a descriptor cannot
// be recovered from an arbitrary net.Conn. A connection therefore has to carry
// its own descriptor, which is what DialPlain returns.
//
// Standard Go builds do not need the descriptor and accept any net.Conn.
type UpgradableConn interface {
	net.Conn

	// Fd returns the underlying descriptor. Upgrade takes ownership of it on
	// success, so callers must not close it themselves afterwards.
	Fd() int
}

// ErrNotUpgradable reports that a connection carries no descriptor, so TLS
// cannot be started on it. Use DialPlain to obtain one that can.
var ErrNotUpgradable = errNotUpgradable

var errNotUpgradable = &upgradeUnsupportedError{}

type upgradeUnsupportedError struct{}

func (*upgradeUnsupportedError) Error() string {
	return "https: connection carries no descriptor; use https.DialPlain"
}
