//go:build (tinygo || force_tinygo_logic) && darwin

package https

/*
// Compiler and linker flags live in cgoflags_darwin_*.go, because TinyGo and
// host Go need different ones.
#include <stdlib.h>
#include "tls_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
	"unsafe"
)

const backendName = "network"

// dialTLS opens a verified TLS connection using Network.framework, which owns
// DNS, TCP, and TLS for this path.
func dialTLS(ctx context.Context, host, port string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	if len(cfg.certificates()) > 0 {
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

	ders, err := cfg.rootCADER()
	if err != nil {
		return nil, err
	}

	caOnly := 0
	if cfg != nil && cfg.RootCAsOnly {
		caOnly = 1
	}

	// The list is created even when empty, whenever RootCAsOnly is set:
	// SecTrustSetAnchorCertificates treats a NULL array as "restore the
	// defaults", so an empty array is what actually trusts nothing.
	var calist C.uintptr_t
	if len(ders) > 0 || caOnly == 1 {
		calist = C.https_nw_calist_new()
		if calist == 0 {
			return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: ErrHandshakeFailed}
		}
		for _, der := range ders {
			rc := C.https_nw_calist_add(calist, unsafe.Pointer(&der[0]), C.int(len(der)))
			if rc != C.HTTPS_NW_OK {
				C.https_nw_calist_free(calist)
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

	chost := C.CString(host)
	cport := C.CString(port)
	cname := C.CString(serverName)
	defer C.free(unsafe.Pointer(chost))
	defer C.free(unsafe.Pointer(cport))
	defer C.free(unsafe.Pointer(cname))

	skip := 0
	if cfg != nil && cfg.InsecureSkipVerify {
		skip = 1
	}

	var handle C.uintptr_t
	var status C.int
	rc := C.https_nw_dial(chost, cport, cname, C.int(skip), calist, C.int(caOnly),
		C.int(cfg.minVersion()), C.int64_t(timeout.Nanoseconds()), &handle, &status)
	if rc != C.HTTPS_NW_OK {
		if calist != 0 {
			C.https_nw_calist_free(calist)
		}
		return nil, dialError(host, int(rc), int(status))
	}

	return &nwConn{handle: handle, host: host, port: port}, nil
}

func (c *Config) certificates() []KeyPair {
	if c == nil {
		return nil
	}
	return c.Certificates
}

// nwConn adapts a Network.framework connection to net.Conn.
type nwConn struct {
	mu     sync.Mutex
	handle C.uintptr_t
	host   string
	port   string

	readDeadline  time.Time
	writeDeadline time.Time
}

var _ net.Conn = (*nwConn)(nil)

// defaultOpTimeout bounds a single read or write when no deadline is set, so a
// stalled peer cannot block a goroutine forever.
const defaultOpTimeout = 5 * time.Minute

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

func (c *nwConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handle == 0 {
		return 0, net.ErrClosed
	}
	ns, ok := timeoutNanos(c.readDeadline)
	if !ok {
		return 0, &Error{Op: "read", Host: c.host, Backend: backendName, Err: errTimeout}
	}

	var n, status C.int
	rc := C.https_nw_recv(c.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(ns), &n, &status)
	if rc != C.HTTPS_NW_OK {
		return 0, ioError("read", c.host, int(rc), int(status))
	}
	if n == 0 {
		return 0, io.EOF
	}
	return int(n), nil
}

func (c *nwConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handle == 0 {
		return 0, net.ErrClosed
	}
	ns, ok := timeoutNanos(c.writeDeadline)
	if !ok {
		return 0, &Error{Op: "write", Host: c.host, Backend: backendName, Err: errTimeout}
	}

	var status C.int
	rc := C.https_nw_send(c.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(ns), &status)
	if rc != C.HTTPS_NW_OK {
		return 0, ioError("write", c.host, int(rc), int(status))
	}
	return len(p), nil
}

func (c *nwConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handle == 0 {
		return nil
	}
	C.https_nw_close(c.handle)
	c.handle = 0
	return nil
}

func (c *nwConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline = t
	c.writeDeadline = t
	return nil
}

func (c *nwConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline = t
	return nil
}

func (c *nwConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeDeadline = t
	return nil
}

// LocalAddr is not exposed by nw_connection in a form worth plumbing through;
// callers of an HTTP client do not depend on it.
func (c *nwConn) LocalAddr() net.Addr { return placeholderAddr("") }

func (c *nwConn) RemoteAddr() net.Addr { return placeholderAddr(net.JoinHostPort(c.host, c.port)) }

type placeholderAddr string

func (placeholderAddr) Network() string  { return "tcp" }
func (a placeholderAddr) String() string { return string(a) }

// Secure Transport OSStatus values reported through nw_error_get_error_code.
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
	case int(C.HTTPS_NW_ERR_TIMEOUT):
		err.Err = errTimeout
	case int(C.HTTPS_NW_ERR_HANDSHAKE):
		err.Err = classifyOSStatus(status)
	case int(C.HTTPS_NW_ERR_CLOSED):
		err.Err = net.ErrClosed
	default:
		err.Err = ErrHandshakeFailed
	}
	return err
}

func ioError(op, host string, rc, status int) error {
	err := &Error{Op: op, Host: host, Backend: backendName, Code: status}
	switch rc {
	case int(C.HTTPS_NW_ERR_TIMEOUT):
		err.Err = errTimeout
	case int(C.HTTPS_NW_ERR_CLOSED):
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
