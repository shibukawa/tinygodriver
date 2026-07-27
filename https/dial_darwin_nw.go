//go:build (tinygo || force_tinygo_logic) && darwin && !darwinstarttlswith13

package https

/*
// Compiler and linker flags live in cgoflags_darwin_*.go, because TinyGo and
// host Go need different ones.
#include <stdlib.h>
#include "tls_darwin_nw.h"
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

// dialNetworkFramework is the default dial path. nw_connection performs DNS,
// TCP and TLS itself, which is why it reaches TLS 1.3 and also why it cannot
// serve upgradeTLS: it has no way to adopt a socket that already carried
// plaintext.
func dialNetworkFramework(ctx context.Context, host, port string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	if len(cfg.certificates()) > 0 {
		return nil, &Error{
			Op: "dial", Host: host, Backend: backendNetwork,
			Err: ErrClientCertificateUnsupported,
		}
	}

	timeout = effectiveTimeout(ctx, timeout)
	if timeout <= 0 {
		return nil, &Error{Op: "dial", Host: host, Backend: backendNetwork, Err: errTimeout}
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
		calist = C.https_nw_calist_new()
		if calist == 0 {
			return nil, &Error{Op: "dial", Host: host, Backend: backendNetwork, Err: ErrHandshakeFailed}
		}
		for _, der := range ders {
			if rc := C.https_nw_calist_add(calist, unsafe.Pointer(&der[0]), C.int(len(der))); rc != C.HTTPS_NW_OK {
				C.https_nw_calist_free(calist)
				return nil, &Error{
					Op: "dial", Host: host, Backend: backendNetwork,
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
		return nil, nwDialError(host, int(rc), int(status))
	}

	return &nwConn{handle: handle, host: host, port: port}, nil
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
		return 0, &Error{Op: "read", Host: c.host, Backend: backendNetwork, Err: errTimeout}
	}

	var n, status C.int
	rc := C.https_nw_recv(c.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(ns), &n, &status)
	if rc != C.HTTPS_NW_OK {
		return 0, nwIOError("read", c.host, int(rc), int(status))
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
		return 0, &Error{Op: "write", Host: c.host, Backend: backendNetwork, Err: errTimeout}
	}

	var status C.int
	rc := C.https_nw_send(c.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(ns), &status)
	if rc != C.HTTPS_NW_OK {
		return 0, nwIOError("write", c.host, int(rc), int(status))
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
	c.readDeadline, c.writeDeadline = t, t
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

func (c *nwConn) RemoteAddr() net.Addr {
	return placeholderAddr(net.JoinHostPort(c.host, c.port))
}

func nwDialError(host string, rc, status int) error {
	err := &Error{Op: "dial", Host: host, Backend: backendNetwork, Code: status}
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

func nwIOError(op, host string, rc, status int) error {
	err := &Error{Op: op, Host: host, Backend: backendNetwork, Code: status}
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
