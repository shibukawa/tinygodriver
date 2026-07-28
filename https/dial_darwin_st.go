//go:build (tinygo || force_tinygo_logic) && darwin && !darwinstarttlswith13

package https

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/shibukawa/tinygodriver/internal/securetransport"
	"github.com/shibukawa/tinygodriver/netdev"
)

// upgradeSecureTransport wraps an already connected socket in TLS. Secure
// Transport is used rather than Network.framework because it is a byte
// transformer with caller-supplied I/O, so the socket may already have carried
// plaintext. That is what an in-band upgrade needs and what an nw_connection
// cannot do.
//
// The trade is TLS 1.2: Apple never added 1.3 to Secure Transport. Build with
// -tags darwinstarttlswith13 to use mbedTLS here instead.
//
// On success the returned net.Conn owns fd. On failure fd is untouched so the
// caller can still fall back to plaintext.
func upgradeSecureTransport(fd int, host string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	if len(cfg.certificates()) > 0 {
		// Same limitation as Network.framework: an identity has to come from a
		// keychain, which this package will not create.
		return nil, &Error{
			Op: "upgrade", Host: host, Backend: backendSecureTransport,
			Err: ErrClientCertificateUnsupported,
		}
	}
	if timeout <= 0 {
		return nil, &Error{Op: "upgrade", Host: host, Backend: backendSecureTransport, Err: errTimeout}
	}
	if cfg.minVersion() > VersionTLS12 {
		// Secure Transport has no TLS 1.3. Clamping quietly would hand back a
		// weaker connection than the caller asked for, so refuse instead.
		return nil, &Error{
			Op: "upgrade", Host: host, Backend: backendSecureTransport,
			Err: ErrProtocolVersion,
		}
	}

	ders, err := cfg.rootCADER()
	if err != nil {
		return nil, err
	}

	opt := securetransport.Options{Host: host, RootCAsDER: ders}
	if cfg != nil {
		if cfg.ServerName != "" {
			opt.Host = cfg.ServerName
		}
		opt.RootCAsOnly = cfg.RootCAsOnly
		opt.SkipVerify = cfg.InsecureSkipVerify
	}

	sess, sErr := securetransport.Handshake(fd, opt, timeout.Nanoseconds())
	if sErr != nil {
		return nil, stUpgradeError(host, sErr)
	}
	return &stConn{sess: sess, fd: fd, dev: socketDialer(), host: host}, nil
}

// stConn adapts a Secure Transport session to net.Conn. It owns the descriptor
// it was handed.
type stConn struct {
	// ioMu serializes access to the session; Secure Transport does not promise
	// a context is safe for concurrent use. state carries the deadlines behind
	// its own lock so SetDeadline never waits for a blocked read.
	ioMu  sync.Mutex
	state connState

	sess *securetransport.Session
	fd   int
	dev  *netdev.Device
	host string
}

var _ net.Conn = (*stConn)(nil)

func (c *stConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ns, err := c.state.readBudget()
	if err != nil {
		return 0, stateError("read", c.host, backendSecureTransport, err)
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	n, sErr := c.sess.Read(p, ns)
	if sErr != nil {
		return 0, stIOError("read", c.host, sErr)
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (c *stConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ns, err := c.state.writeBudget()
	if err != nil {
		return 0, stateError("write", c.host, backendSecureTransport, err)
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	n, sErr := c.sess.Write(p, ns)
	if sErr != nil {
		return n, stIOError("write", c.host, sErr)
	}
	return n, nil
}

func (c *stConn) Close() error {
	if !c.state.close() {
		return nil
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	c.sess.Close()
	return c.dev.Close(c.fd)
}

func (c *stConn) SetDeadline(t time.Time) error      { return c.state.setDeadline(t) }
func (c *stConn) SetReadDeadline(t time.Time) error  { return c.state.setReadDeadline(t) }
func (c *stConn) SetWriteDeadline(t time.Time) error { return c.state.setWriteDeadline(t) }

func (c *stConn) LocalAddr() net.Addr  { return placeholderAddr("") }
func (c *stConn) RemoteAddr() net.Addr { return placeholderAddr(c.host) }

func stUpgradeError(host string, e *securetransport.Error) error {
	err := &Error{Op: "upgrade", Host: host, Backend: backendSecureTransport, Code: e.Status}
	switch e.Class {
	case securetransport.ClassTimeout:
		err.Err = errTimeout
	case securetransport.ClassHandshake:
		err.Err = classifyOSStatus(e.Status)
	case securetransport.ClassCA:
		err.Err = ErrCertificateInvalid
	case securetransport.ClassClosed:
		err.Err = net.ErrClosed
	default:
		err.Err = ErrHandshakeFailed
	}
	return err
}

func stIOError(op, host string, e *securetransport.Error) error {
	err := &Error{Op: op, Host: host, Backend: backendSecureTransport, Code: e.Status}
	switch e.Class {
	case securetransport.ClassTimeout:
		err.Err = errTimeout
	case securetransport.ClassClosed:
		err.Err = net.ErrClosed
	default:
		if e.Status == errSSLNetworkTimeout {
			err.Err = errTimeout
		} else {
			err.Err = errors.New("https: encrypted I/O failed")
		}
	}
	return err
}
