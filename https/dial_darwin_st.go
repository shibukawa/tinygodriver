//go:build (tinygo || force_tinygo_logic) && darwin && !darwintls13

package https

/*
// Compiler and linker flags live in cgoflags_darwin_st_*.go.
#include <stdlib.h>
#include "tls_darwin_st.h"
*/
import "C"

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/shibukawa/tinygodriver/netdev"
)

const backendName = "securetransport"

// Secure Transport is a byte transformer with caller-supplied I/O, so Go owns
// the socket. TinyGo's net package does not implement SyscallConn, so the
// descriptor comes from netdev directly, the same way the Linux backend gets
// it.
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
	if len(cfg.certificates()) > 0 {
		// Same limitation as the Network.framework backend: an identity has to
		// come from a keychain, which this package will not create.
		return nil, &Error{
			Op: "dial", Host: host, Backend: backendName,
			Err: ErrClientCertificateUnsupported,
		}
	}

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

	ders, err := cfg.rootCADER()
	if err != nil {
		return nil, err
	}

	caOnly := 0
	if cfg != nil && cfg.RootCAsOnly {
		caOnly = 1
	}

	// The list is created even when empty whenever RootCAsOnly is set:
	// SecTrustSetAnchorCertificates treats a NULL array as "restore the
	// defaults", so an empty array is what actually trusts nothing.
	var calist C.uintptr_t
	if len(ders) > 0 || caOnly == 1 {
		calist = C.https_st_calist_new()
		if calist == 0 {
			return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: ErrHandshakeFailed}
		}
		for _, der := range ders {
			if rc := C.https_st_calist_add(calist, unsafe.Pointer(&der[0]), C.int(len(der))); rc != C.HTTPS_ST_OK {
				C.https_st_calist_free(calist)
				return nil, &Error{
					Op: "dial", Host: host, Backend: backendName,
					Err: ErrCertificateInvalid,
				}
			}
		}
	}

	serverName := host
	if cfg != nil && cfg.ServerName != "" {
		serverName = cfg.ServerName
	}

	skip := 0
	if cfg != nil && cfg.InsecureSkipVerify {
		skip = 1
	}

	dev := socketDialer()
	fd, err := dev.Socket(netdev.AF_INET, netdev.SOCK_STREAM, netdev.IPPROTO_TCP)
	if err != nil {
		if calist != 0 {
			C.https_st_calist_free(calist)
		}
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}

	// An invalid address makes netdev resolve host itself.
	addr := netip.AddrPortFrom(netip.Addr{}, uint16(portNum))
	if ip, err := netip.ParseAddr(host); err == nil {
		addr = netip.AddrPortFrom(ip, uint16(portNum))
	}
	if err := dev.Connect(fd, host, addr); err != nil {
		dev.Close(fd)
		if calist != 0 {
			C.https_st_calist_free(calist)
		}
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}

	cname := C.CString(serverName)
	defer C.free(unsafe.Pointer(cname))

	var handle C.uintptr_t
	var status C.int
	rc := C.https_st_handshake(C.int(fd), cname, C.int(skip), calist, C.int(caOnly),
		C.int(secureTransportMinVersion(cfg)), C.int64_t(timeout.Nanoseconds()),
		&handle, &status)
	if rc != C.HTTPS_ST_OK {
		dev.Close(fd)
		if calist != 0 {
			C.https_st_calist_free(calist)
		}
		return nil, dialError(host, int(rc), int(status))
	}

	return &stConn{handle: handle, fd: fd, dev: dev, host: host, port: port}, nil
}

// Secure Transport protocol constants. It stops at TLS 1.2; TLS 1.3 exists
// only in Network.framework, which the darwintls13 build tag selects.
const (
	stTLSProtocol1  = 4
	stTLSProtocol11 = 7
	stTLSProtocol12 = 8
)

func secureTransportMinVersion(cfg *Config) int {
	switch cfg.minVersion() {
	case VersionTLS13:
		// Asking for TLS 1.3 here would be unsatisfiable. Clamping quietly
		// would hand back a weaker connection than requested, so the dial is
		// rejected instead; see ErrProtocolVersion in dialError.
		return -1
	default:
		return stTLSProtocol12
	}
}

func (c *Config) certificates() []KeyPair {
	if c == nil {
		return nil
	}
	return c.Certificates
}

// stConn adapts a Secure Transport session to net.Conn.
type stConn struct {
	mu     sync.Mutex
	handle C.uintptr_t
	fd     int
	dev    *netdev.Device
	host   string
	port   string

	readDeadline  time.Time
	writeDeadline time.Time
	closed        bool
}

var _ net.Conn = (*stConn)(nil)

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
		return 0, &Error{Op: "read", Host: c.host, Backend: backendName, Err: errTimeout}
	}

	var n, status C.int
	rc := C.https_st_read(c.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(ns), &n, &status)
	if rc != C.HTTPS_ST_OK {
		return 0, ioError("read", c.host, int(rc), int(status))
	}
	if n == 0 {
		return 0, io.EOF
	}
	return int(n), nil
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
		return 0, &Error{Op: "write", Host: c.host, Backend: backendName, Err: errTimeout}
	}

	var n, status C.int
	rc := C.https_st_write(c.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(ns), &n, &status)
	if rc != C.HTTPS_ST_OK {
		return int(n), ioError("write", c.host, int(rc), int(status))
	}
	return int(n), nil
}

func (c *stConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	C.https_st_close(c.handle)
	c.handle = 0
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

func (c *stConn) LocalAddr() net.Addr { return placeholderAddr("") }

func (c *stConn) RemoteAddr() net.Addr {
	return placeholderAddr(net.JoinHostPort(c.host, c.port))
}

type placeholderAddr string

func (placeholderAddr) Network() string  { return "tcp" }
func (a placeholderAddr) String() string { return string(a) }

// Secure Transport OSStatus values.
const (
	errSSLProtocol          = -9800
	errSSLNegotiation       = -9801
	errSSLFatalAlert        = -9802
	errSSLXCertChainInvalid = -9807
	errSSLBadCert           = -9808
	errSSLUnknownRootCert   = -9812
	errSSLNoRootCert        = -9813
	errSSLCertExpired       = -9814
	errSSLCertNotYetValid   = -9815
	errSSLPeerUnknownCA     = -9825
	errSSLPeerAccessDenied  = -9826
	errSSLHostNameMismatch  = -9843
	errSSLBadConfiguration  = -9848
	errSSLNetworkTimeout    = -9853
)

func dialError(host string, rc, status int) error {
	err := &Error{Op: "dial", Host: host, Backend: backendName, Code: status}
	switch rc {
	case int(C.HTTPS_ST_ERR_TIMEOUT):
		err.Err = errTimeout
	case int(C.HTTPS_ST_ERR_HANDSHAKE):
		err.Err = classifyOSStatus(status)
	case int(C.HTTPS_ST_ERR_CLOSED):
		err.Err = net.ErrClosed
	case int(C.HTTPS_ST_ERR_SETUP):
		// The only setup failure the caller can act on is an unsatisfiable
		// minimum version, which is how a TLS 1.3 request arrives here.
		err.Err = ErrProtocolVersion
	default:
		err.Err = ErrHandshakeFailed
	}
	return err
}

func ioError(op, host string, rc, status int) error {
	err := &Error{Op: op, Host: host, Backend: backendName, Code: status}
	switch rc {
	case int(C.HTTPS_ST_ERR_TIMEOUT):
		err.Err = errTimeout
	case int(C.HTTPS_ST_ERR_CLOSED):
		err.Err = net.ErrClosed
	default:
		if status == errSSLNetworkTimeout {
			err.Err = errTimeout
		} else {
			err.Err = errors.New("https: encrypted I/O failed")
		}
	}
	return err
}

func classifyOSStatus(status int) error {
	switch status {
	case errSSLHostNameMismatch:
		return ErrHostnameMismatch
	case errSSLCertExpired, errSSLCertNotYetValid:
		return ErrCertificateExpired
	case errSSLXCertChainInvalid, errSSLUnknownRootCert, errSSLNoRootCert, errSSLPeerUnknownCA:
		return ErrUntrustedRoot
	case errSSLBadCert:
		return ErrCertificateInvalid
	case errSSLPeerAccessDenied:
		return ErrClientCertificateRejected
	case errSSLProtocol, errSSLBadConfiguration:
		return ErrProtocolVersion
	case errSSLNetworkTimeout:
		return errTimeout
	case errSSLNegotiation, errSSLFatalAlert:
		return ErrHandshakeFailed
	default:
		return ErrHandshakeFailed
	}
}
