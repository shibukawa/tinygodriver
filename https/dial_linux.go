//go:build (tinygo || force_tinygo_logic) && linux

package https

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/shibukawa/tinygodriver/https/internal/mbedtls"
	"github.com/shibukawa/tinygodriver/netdev"
)

const backendName = "mbedtls"

// The socket comes from netdev rather than net.Dial because TinyGo's net
// package does not implement SyscallConn, so there is no way to recover the
// file descriptor from a net.Conn. netdev.Device exposes the descriptor
// directly, and it is the same driver TinyGo's net package uses anyway.
var (
	dialerOnce sync.Once
	dialer     *netdev.Device
)

func socketDialer() *netdev.Device {
	dialerOnce.Do(func() { dialer = netdev.New() })
	return dialer
}

// defaultOpTimeout bounds a single read or write when no deadline is set, so a
// stalled peer cannot block a goroutine forever.
const defaultOpTimeout = 5 * time.Minute

func dialTLS(ctx context.Context, host, port string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: errTimeout}
	}

	portNum, err := strconv.Atoi(port)
	if err != nil {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}

	opt := mbedtls.Options{
		Host:       host,
		SkipVerify: cfg.skipVerify(),
		MinVersion: uint16(cfg.minVersion()),
	}
	if cfg != nil && cfg.ServerName != "" {
		opt.Host = cfg.ServerName
	}
	if !opt.SkipVerify {
		anchors, err := anchorsFor(cfg)
		if err != nil {
			return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
		}
		opt.RootCAsPEM = anchors
	}
	if certs := cfg.certificates(); len(certs) > 0 {
		// mbedTLS takes PEM directly, so mutual TLS works here even though the
		// darwin backend has to refuse it.
		opt.ClientCertPEM = certs[0].CertPEM
		opt.ClientKeyPEM = certs[0].KeyPEM
	}

	dev := socketDialer()
	fd, err := dev.Socket(netdev.AF_INET, netdev.SOCK_STREAM, netdev.IPPROTO_TCP)
	if err != nil {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}

	// An invalid address makes netdev resolve host itself.
	addr := netip.AddrPortFrom(netip.Addr{}, uint16(portNum))
	if ip, err := netip.ParseAddr(host); err == nil {
		addr = netip.AddrPortFrom(ip, uint16(portNum))
	}
	if err := dev.Connect(fd, host, addr); err != nil {
		dev.Close(fd)
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}

	sess, mErr := mbedtls.Handshake(fd, opt, timeout.Nanoseconds())
	if mErr != nil {
		dev.Close(fd)
		return nil, dialError(host, mErr)
	}

	return &mbedConn{sess: sess, fd: fd, dev: dev, host: host, port: port}, nil
}

func (c *Config) skipVerify() bool {
	return c != nil && c.InsecureSkipVerify
}

func (c *Config) certificates() []KeyPair {
	if c == nil {
		return nil
	}
	return c.Certificates
}

// mbedConn adapts an mbedTLS session to net.Conn.
type mbedConn struct {
	mu   sync.Mutex
	sess *mbedtls.Session
	fd   int
	dev  *netdev.Device
	host string
	port string

	readDeadline  time.Time
	writeDeadline time.Time
	closed        bool
}

var _ net.Conn = (*mbedConn)(nil)

func timeoutNanos(deadline time.Time) (int64, bool) {
	if deadline.IsZero() {
		return int64(defaultOpTimeout), true
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, false
	}
	return remaining.Nanoseconds(), true
}

func (c *mbedConn) Read(p []byte) (int, error) {
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
		return 0, &Error{Op: "read", Host: c.host, Backend: backendName, Err: errTimeout}
	}

	n, mErr := c.sess.Read(p, ns)
	if mErr != nil {
		return 0, ioError("read", c.host, mErr)
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (c *mbedConn) Write(p []byte) (int, error) {
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
		return 0, &Error{Op: "write", Host: c.host, Backend: backendName, Err: errTimeout}
	}

	n, mErr := c.sess.Write(p, ns)
	if mErr != nil {
		return n, ioError("write", c.host, mErr)
	}
	return n, nil
}

func (c *mbedConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.sess.Close()
	return c.dev.Close(c.fd)
}

func (c *mbedConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline, c.writeDeadline = t, t
	return nil
}

func (c *mbedConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline = t
	return nil
}

func (c *mbedConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeDeadline = t
	return nil
}

func (c *mbedConn) LocalAddr() net.Addr { return placeholderAddr("") }

func (c *mbedConn) RemoteAddr() net.Addr {
	return placeholderAddr(net.JoinHostPort(c.host, c.port))
}

type placeholderAddr string

func (placeholderAddr) Network() string  { return "tcp" }
func (a placeholderAddr) String() string { return string(a) }

// dialError maps a handshake failure onto a sentinel. mbedTLS reports every
// verification problem as one coarse status, so the verify bitmask is what
// distinguishes an expired certificate from a name mismatch.
func dialError(host string, e *mbedtls.Error) error {
	out := &Error{Op: "dial", Host: host, Backend: backendName, Code: e.Code}

	switch e.Class {
	case mbedtls.ClassTimeout:
		out.Err = errTimeout
		return out
	case mbedtls.ClassCA:
		out.Err = ErrCertificateInvalid
		return out
	case mbedtls.ClassClientCert:
		out.Err = ErrClientCertificateRejected
		return out
	}

	switch {
	case e.VerifyFlags&mbedtls.BadCertCNMismatch != 0:
		out.Err = ErrHostnameMismatch
	case e.VerifyFlags&(mbedtls.BadCertExpired|mbedtls.BadCertFuture) != 0:
		out.Err = ErrCertificateExpired
	case e.VerifyFlags&mbedtls.BadCertNotTrusted != 0:
		out.Err = ErrUntrustedRoot
	case e.VerifyFlags&mbedtls.BadCertRevoked != 0:
		out.Err = ErrCertificateInvalid
	case e.VerifyFlags != 0:
		out.Err = ErrCertificateInvalid
	default:
		out.Err = ErrHandshakeFailed
	}
	return out
}

func ioError(op, host string, e *mbedtls.Error) error {
	out := &Error{Op: op, Host: host, Backend: backendName, Code: e.Code}
	switch e.Class {
	case mbedtls.ClassTimeout:
		out.Err = errTimeout
	case mbedtls.ClassClosed:
		out.Err = net.ErrClosed
	default:
		out.Err = errors.New("https: encrypted I/O failed")
	}
	return out
}
