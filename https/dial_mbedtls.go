//go:build (tinygo || force_tinygo_logic) && (linux || (darwin && darwinstarttlswith13))

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

	"github.com/shibukawa/tinygodriver/internal/mbedtls"
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

func dialTLS(ctx context.Context, host, port string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	timeout = effectiveTimeout(ctx, timeout)
	if timeout <= 0 {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: errTimeout}
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

	fd, dev, err := dialSocket(host, port)
	if err != nil {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}

	sess, mErr := mbedtls.Handshake(fd, opt, timeout.Nanoseconds())
	if mErr != nil {
		dev.Close(fd)
		return nil, dialError(host, mErr)
	}

	return &mbedConn{sess: sess, fd: fd, dev: dev, host: host, port: port}, nil
}

// dialSocket connects a TCP socket and returns its descriptor. An invalid
// address makes netdev resolve host itself.
func dialSocket(host, port string) (int, *netdev.Device, error) {
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return -1, nil, err
	}
	dev := socketDialer()
	fd, err := dev.Socket(netdev.AF_INET, netdev.SOCK_STREAM, netdev.IPPROTO_TCP)
	if err != nil {
		return -1, nil, err
	}
	addr := netip.AddrPortFrom(netip.Addr{}, uint16(portNum))
	if ip, err := netip.ParseAddr(host); err == nil {
		addr = netip.AddrPortFrom(ip, uint16(portNum))
	}
	if err := dev.Connect(fd, host, addr); err != nil {
		dev.Close(fd)
		return -1, nil, err
	}
	return fd, dev, nil
}

// upgradeTLS wraps an already connected socket in TLS, which is what in-band
// upgrade protocols such as PostgreSQL and MySQL STARTTLS need. The descriptor
// may already have carried plaintext.
//
// mbedTLS reaches the socket through BIO callbacks, so unlike
// Network.framework it does not care what the stream carried before. This path
// therefore keeps TLS 1.3, which is the whole point of the
// darwinstarttlswith13 build on macOS.
//
// On success the returned net.Conn owns fd. On failure fd is untouched so the
// caller can still fall back to plaintext.
func upgradeTLS(fd int, host string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		return nil, &Error{Op: "upgrade", Host: host, Backend: backendName, Err: errTimeout}
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
			return nil, &Error{Op: "upgrade", Host: host, Backend: backendName, Err: err}
		}
		opt.RootCAsPEM = anchors
	}
	if certs := cfg.certificates(); len(certs) > 0 {
		opt.ClientCertPEM = certs[0].CertPEM
		opt.ClientKeyPEM = certs[0].KeyPEM
	}

	sess, mErr := mbedtls.Handshake(fd, opt, timeout.Nanoseconds())
	if mErr != nil {
		return nil, upgradeError(host, mErr)
	}
	return &mbedConn{sess: sess, fd: fd, dev: socketDialer(), host: host}, nil
}

// upgradeError mirrors dialError but reports the upgrade operation.
func upgradeError(host string, e *mbedtls.Error) error {
	err := dialError(host, e).(*Error)
	err.Op = "upgrade"
	return err
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
	// ioMu serializes access to the session; state carries the deadlines behind
	// its own lock so SetDeadline never waits for a blocked read.
	ioMu  sync.Mutex
	state connState

	sess *mbedtls.Session
	fd   int
	dev  *netdev.Device
	host string
	port string
}

var _ net.Conn = (*mbedConn)(nil)

func (c *mbedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ns, err := c.state.readBudget()
	if err != nil {
		return 0, stateError("read", c.host, backendName, err)
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()

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
	ns, err := c.state.writeBudget()
	if err != nil {
		return 0, stateError("write", c.host, backendName, err)
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	n, mErr := c.sess.Write(p, ns)
	if mErr != nil {
		return n, ioError("write", c.host, mErr)
	}
	return n, nil
}

func (c *mbedConn) Close() error {
	if !c.state.close() {
		return nil
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	c.sess.Close()
	return c.dev.Close(c.fd)
}

func (c *mbedConn) SetDeadline(t time.Time) error      { return c.state.setDeadline(t) }
func (c *mbedConn) SetReadDeadline(t time.Time) error  { return c.state.setReadDeadline(t) }
func (c *mbedConn) SetWriteDeadline(t time.Time) error { return c.state.setWriteDeadline(t) }

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
