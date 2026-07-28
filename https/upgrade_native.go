//go:build (tinygo || force_tinygo_logic) && (darwin || linux || windows)

package https

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/shibukawa/tinygodriver/netdev"
)

// DialPlain opens a plaintext TCP connection suitable for Upgrade.
//
// The connection is a netdev socket rather than net.Dial, because TinyGo's net
// package does not implement SyscallConn and Upgrade needs the descriptor.
func DialPlain(ctx context.Context, host, port string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}
	fd, dev, err := dialSocket(host, port)
	if err != nil {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}
	return &plainConn{fd: fd, dev: dev, host: host, port: port}, nil
}

// Upgrade starts TLS on a connection that has already carried plaintext, and
// returns a net.Conn whose bytes are plaintext again.
//
// host is the SNI name and the name verified against the certificate;
// Config.ServerName overrides it. Verification completes before Upgrade
// returns, so a returned connection is a verified peer.
//
// conn must carry a descriptor, which means it must come from DialPlain or
// implement UpgradableConn. Anything else returns ErrNotUpgradable.
//
// On success the returned connection owns the descriptor and closing it closes
// the descriptor; conn must not be closed as well. On failure the descriptor is
// untouched and conn stays usable, so a caller that wants to continue in
// plaintext still can.
//
// The handshake is bounded by the context deadline when there is one, and by
// defaultOpTimeout when there is not.
func Upgrade(ctx context.Context, conn net.Conn, host string, cfg *Config) (net.Conn, error) {
	if cfg != nil && cfg.err != nil {
		return nil, cfg.err
	}
	uc, ok := conn.(UpgradableConn)
	if !ok {
		return nil, &Error{Op: "upgrade", Host: host, Backend: backendName, Err: ErrNotUpgradable}
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Op: "upgrade", Host: host, Backend: backendName, Err: err}
	}

	timeout := effectiveTimeout(ctx, defaultOpTimeout)
	if timeout <= 0 {
		return nil, &Error{Op: "upgrade", Host: host, Backend: backendName, Err: errTimeout}
	}

	tlsConn, err := upgradeTLS(uc.Fd(), host, cfg, timeout)
	if err != nil {
		// The descriptor is untouched, so conn remains the owner.
		return nil, err
	}
	// Ownership moved to tlsConn. Detach conn so a deferred Close on it cannot
	// close a descriptor the TLS connection is still using.
	if p, ok := uc.(*plainConn); ok {
		p.release()
	}
	// An upgrade does not change which peer is on the other end, so the
	// addresses must survive it. The backends build their conn from a bare
	// descriptor and cannot recover the port, and a RemoteAddr without one
	// breaks any caller that reconnects to the same server -- PostgreSQL's
	// CancelRequest does exactly that.
	return &upgradedConn{Conn: tlsConn, local: conn.LocalAddr(), remote: conn.RemoteAddr()}, nil
}

// upgradedConn keeps the pre-upgrade addresses on the TLS connection.
type upgradedConn struct {
	net.Conn
	local  net.Addr
	remote net.Addr
}

func (c *upgradedConn) LocalAddr() net.Addr  { return c.local }
func (c *upgradedConn) RemoteAddr() net.Addr { return c.remote }

// plainConn is a plaintext netdev socket that carries its descriptor, so
// Upgrade can start TLS on it.
type plainConn struct {
	// ioMu serializes reads against reads and writes against writes; state
	// carries the deadlines behind its own lock so SetDeadline never waits for
	// a blocked read.
	ioMu  sync.Mutex
	state connState

	fd   int
	dev  *netdev.Device
	host string
	port string

	relMu    sync.Mutex
	released bool
}

var (
	_ net.Conn       = (*plainConn)(nil)
	_ UpgradableConn = (*plainConn)(nil)
)

// Fd returns the descriptor. It stays valid until Close, or until Upgrade
// takes ownership.
func (c *plainConn) Fd() int { return c.fd }

// release hands the descriptor to another owner, so Close becomes a no-op.
func (c *plainConn) release() {
	c.relMu.Lock()
	defer c.relMu.Unlock()
	c.released = true
}

func (c *plainConn) isReleased() bool {
	c.relMu.Lock()
	defer c.relMu.Unlock()
	return c.released
}

func (c *plainConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	readDeadline, _, err := c.state.deadlines()
	if err != nil {
		return 0, err
	}
	if c.isReleased() {
		return 0, net.ErrClosed
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	n, err := c.dev.Recv(c.fd, p, 0, readDeadline)
	if err != nil {
		if err == io.EOF {
			return 0, io.EOF
		}
		return 0, &Error{Op: "read", Host: c.host, Backend: backendName, Err: err}
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (c *plainConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	_, writeDeadline, err := c.state.deadlines()
	if err != nil {
		return 0, err
	}
	if c.isReleased() {
		return 0, net.ErrClosed
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	// netdev.Send may return short, so loop until the buffer is drained.
	written := 0
	for written < len(p) {
		n, err := c.dev.Send(c.fd, p[written:], 0, writeDeadline)
		if err != nil {
			return written, &Error{Op: "write", Host: c.host, Backend: backendName, Err: err}
		}
		if n <= 0 {
			return written, &Error{Op: "write", Host: c.host, Backend: backendName, Err: io.ErrShortWrite}
		}
		written += n
	}
	return written, nil
}

// Close releases the descriptor, unless Upgrade already took ownership of it.
func (c *plainConn) Close() error {
	if c.isReleased() {
		return nil
	}
	if !c.state.close() {
		return nil
	}
	return c.dev.Close(c.fd)
}

func (c *plainConn) SetDeadline(t time.Time) error      { return c.state.setDeadline(t) }
func (c *plainConn) SetReadDeadline(t time.Time) error  { return c.state.setReadDeadline(t) }
func (c *plainConn) SetWriteDeadline(t time.Time) error { return c.state.setWriteDeadline(t) }

func (c *plainConn) LocalAddr() net.Addr { return placeholderAddr("") }

func (c *plainConn) RemoteAddr() net.Addr {
	return placeholderAddr(net.JoinHostPort(c.host, c.port))
}
