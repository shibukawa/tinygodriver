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
	mu   sync.Mutex
	sess *securetransport.Session
	fd   int
	dev  *netdev.Device
	host string

	readDeadline  time.Time
	writeDeadline time.Time
	closed        bool
}

var _ net.Conn = (*stConn)(nil)

func (c *stConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	ns, ok := timeoutNanos(c.readDeadline)
	if !ok {
		return 0, &Error{Op: "read", Host: c.host, Backend: backendSecureTransport, Err: errTimeout}
	}

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
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	ns, ok := timeoutNanos(c.writeDeadline)
	if !ok {
		return 0, &Error{Op: "write", Host: c.host, Backend: backendSecureTransport, Err: errTimeout}
	}

	n, sErr := c.sess.Write(p, ns)
	if sErr != nil {
		return n, stIOError("write", c.host, sErr)
	}
	return n, nil
}

func (c *stConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.sess.Close()
	return c.dev.Close(c.fd)
}

func (c *stConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline, c.writeDeadline = t, t
	return nil
}

func (c *stConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline = t
	return nil
}

func (c *stConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeDeadline = t
	return nil
}

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
