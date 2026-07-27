//go:build darwin

// Package securetransport wraps macOS Secure Transport for the packages in
// this repository that need TLS over a file descriptor they already own.
//
// It is deprecated by Apple in favour of Network.framework, but it is the only
// OS-provided stack that fits: an nw_connection owns DNS, TCP and TLS as one
// unit and cannot adopt an existing socket, while Secure Transport is a byte
// transformer with caller-supplied I/O. That is what both callers need, and it
// is what an in-band upgrade such as STARTTLS requires.
//
// The trade is TLS 1.2: Apple never added 1.3 here.
package securetransport

/*
#include <stdlib.h>
#include "securetransport.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Supported reports whether this build has the backend compiled in.
const Supported = true

// TLS protocol constants. Secure Transport stops at 1.2.
const (
	ProtocolTLS12 = 8
)

// Options configures one handshake.
type Options struct {
	// Host is the SNI name and the name verified against the certificate.
	Host string

	// RootCAsDER are additional trust anchors, DER encoded. When empty the
	// system keychain is used.
	RootCAsDER [][]byte

	// RootCAsOnly ignores the system trust store, trusting only RootCAsDER.
	// With no anchors set this trusts nothing, which is the intent.
	RootCAsOnly bool

	SkipVerify bool
}

// Session is a completed TLS session over a caller-owned descriptor.
// It is not safe for concurrent use.
type Session struct {
	handle C.uintptr_t
}

// Class is the coarse failure category from the C layer.
type Class int

const (
	ClassOK Class = iota
	ClassAlloc
	ClassSetup
	ClassCA
	ClassClientCert
	ClassHandshake
	ClassTimeout
	ClassIO
	ClassClosed
)

// Error carries the class and the raw OSStatus.
type Error struct {
	Class  Class
	Status int
}

func (e *Error) Error() string {
	return fmt.Sprintf("securetransport: class %d, OSStatus %d", e.Class, e.Status)
}

// Timeout reports whether this was a deadline expiry.
func (e *Error) Timeout() bool { return e.Class == ClassTimeout }

// Handshake performs the TLS handshake over an already connected socket. The
// descriptor stays owned by the caller; Close does not close it.
//
// The socket may already have carried plaintext, which is what makes an
// in-band upgrade possible.
func Handshake(fd int, opt Options, timeoutNanos int64) (*Session, *Error) {
	host := C.CString(opt.Host)
	defer C.free(unsafe.Pointer(host))

	caOnly := 0
	if opt.RootCAsOnly {
		caOnly = 1
	}
	skip := 0
	if opt.SkipVerify {
		skip = 1
	}

	// The list is created even when empty whenever RootCAsOnly is set:
	// SecTrustSetAnchorCertificates treats a NULL array as "restore the
	// defaults", so an empty array is what actually trusts nothing.
	var calist C.uintptr_t
	if len(opt.RootCAsDER) > 0 || caOnly == 1 {
		calist = C.https_st_calist_new()
		if calist == 0 {
			return nil, &Error{Class: ClassAlloc}
		}
		for _, der := range opt.RootCAsDER {
			if len(der) == 0 {
				continue
			}
			if rc := C.https_st_calist_add(calist, unsafe.Pointer(&der[0]), C.int(len(der))); rc != C.HTTPS_ST_OK {
				C.https_st_calist_free(calist)
				return nil, &Error{Class: ClassCA}
			}
		}
	}

	var handle C.uintptr_t
	var status C.int
	rc := C.https_st_handshake(C.int(fd), host, C.int(skip), calist, C.int(caOnly),
		C.int(ProtocolTLS12), C.int64_t(timeoutNanos), &handle, &status)
	if rc != C.HTTPS_ST_OK {
		if calist != 0 {
			C.https_st_calist_free(calist)
		}
		return nil, &Error{Class: Class(-int(rc)), Status: int(status)}
	}
	return &Session{handle: handle}, nil
}

// Read decrypts into p. A zero count with a nil error means a clean EOF.
func (s *Session) Read(p []byte, timeoutNanos int64) (int, *Error) {
	if s.handle == 0 {
		return 0, &Error{Class: ClassClosed}
	}
	if len(p) == 0 {
		return 0, nil
	}
	var n, status C.int
	rc := C.https_st_read(s.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(timeoutNanos), &n, &status)
	if rc != C.HTTPS_ST_OK {
		return 0, &Error{Class: Class(-int(rc)), Status: int(status)}
	}
	return int(n), nil
}

// Write encrypts p, reporting how much made it out.
func (s *Session) Write(p []byte, timeoutNanos int64) (int, *Error) {
	if s.handle == 0 {
		return 0, &Error{Class: ClassClosed}
	}
	if len(p) == 0 {
		return 0, nil
	}
	var n, status C.int
	rc := C.https_st_write(s.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(timeoutNanos), &n, &status)
	if rc != C.HTTPS_ST_OK {
		return int(n), &Error{Class: Class(-int(rc)), Status: int(status)}
	}
	return int(n), nil
}

// Close releases the session. It does not close the caller's descriptor.
func (s *Session) Close() {
	if s == nil || s.handle == 0 {
		return
	}
	C.https_st_close(s.handle)
	s.handle = 0
}
