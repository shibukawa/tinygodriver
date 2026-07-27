//go:build (tinygo || force_tinygo_logic) && darwin && !darwinstarttlswith13

package https

/*
// Compiler and linker flags live in cgoflags_darwin_*.go.
#include <stdlib.h>
#include "tls_darwin_st.h"
*/
import "C"

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"
	"unsafe"

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
		// weaker connection than the caller asked for, so refuse instead. Build
		// with -tags darwinstarttlswith13 to get 1.3 on this path.
		return nil, &Error{
			Op: "upgrade", Host: host, Backend: backendSecureTransport,
			Err: ErrProtocolVersion,
		}
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
			return nil, &Error{Op: "upgrade", Host: host, Backend: backendSecureTransport, Err: ErrHandshakeFailed}
		}
		for _, der := range ders {
			if rc := C.https_st_calist_add(calist, unsafe.Pointer(&der[0]), C.int(len(der))); rc != C.HTTPS_ST_OK {
				C.https_st_calist_free(calist)
				return nil, &Error{
					Op: "upgrade", Host: host, Backend: backendSecureTransport,
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

	cname := C.CString(serverName)
	defer C.free(unsafe.Pointer(cname))

	var handle C.uintptr_t
	var status C.int
	rc := C.https_st_handshake(C.int(fd), cname, C.int(skip), calist, C.int(caOnly),
		C.int(secureTransportMinVersion(cfg)), C.int64_t(timeout.Nanoseconds()),
		&handle, &status)
	if rc != C.HTTPS_ST_OK {
		if calist != 0 {
			C.https_st_calist_free(calist)
		}
		return nil, stUpgradeError(host, int(rc), int(status))
	}

	return &stConn{handle: handle, fd: fd, dev: socketDialer(), host: host}, nil
}

// Secure Transport protocol constants. It stops at TLS 1.2; TLS 1.3 on the
// upgrade path requires -tags darwinstarttlswith13.
const (
	stTLSProtocol1  = 4
	stTLSProtocol11 = 7
	stTLSProtocol12 = 8
)

// Anything above TLS 1.2 is rejected before reaching here.
func secureTransportMinVersion(cfg *Config) int {
	switch cfg.minVersion() {
	case VersionTLS12:
		return stTLSProtocol12
	default:
		return stTLSProtocol12
	}
}

// stConn adapts a Secure Transport session to net.Conn. It owns the descriptor
// it was handed.
type stConn struct {
	mu     sync.Mutex
	handle C.uintptr_t
	fd     int
	dev    *netdev.Device
	host   string

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

	var n, status C.int
	rc := C.https_st_read(c.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(ns), &n, &status)
	if rc != C.HTTPS_ST_OK {
		return 0, stIOError("read", c.host, int(rc), int(status))
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
		return 0, &Error{Op: "write", Host: c.host, Backend: backendSecureTransport, Err: errTimeout}
	}

	var n, status C.int
	rc := C.https_st_write(c.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(ns), &n, &status)
	if rc != C.HTTPS_ST_OK {
		return int(n), stIOError("write", c.host, int(rc), int(status))
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

func (c *stConn) LocalAddr() net.Addr  { return placeholderAddr("") }
func (c *stConn) RemoteAddr() net.Addr { return placeholderAddr(c.host) }

func stUpgradeError(host string, rc, status int) error {
	err := &Error{Op: "upgrade", Host: host, Backend: backendSecureTransport, Code: status}
	switch rc {
	case int(C.HTTPS_ST_ERR_TIMEOUT):
		err.Err = errTimeout
	case int(C.HTTPS_ST_ERR_HANDSHAKE):
		err.Err = classifyOSStatus(status)
	case int(C.HTTPS_ST_ERR_CLOSED):
		err.Err = net.ErrClosed
	default:
		err.Err = ErrHandshakeFailed
	}
	return err
}

func stIOError(op, host string, rc, status int) error {
	err := &Error{Op: op, Host: host, Backend: backendSecureTransport, Code: status}
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
