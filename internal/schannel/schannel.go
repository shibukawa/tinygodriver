//go:build windows

// Package schannel wraps the Windows Schannel provider for the packages in
// this repository that need TLS over a socket they already own.
//
// Schannel is reached through SSPI, which is a buffer transformer: it never
// sees the socket, it only produces and consumes token bytes while this
// package moves them. That is what both callers need, and it is what an
// in-band upgrade such as STARTTLS requires.
//
// Unlike the darwin backend this is not a pure-Go binding. The design notes
// for this repository assumed syscall.NewLazyDLL, but TinyGo ships no windows
// syscall implementation at all, so SSPI has to be reached the same way
// netdev/sys_windows.go already reaches winsock: through cgo.
package schannel

/*
#include <stdlib.h>
#include "sspibridge.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Supported reports whether this build has the backend compiled in.
const Supported = true

// TLS protocol versions, as wire values.
const (
	ProtocolTLS12 = 0x0303
	ProtocolTLS13 = 0x0304
)

// Options configures one handshake.
type Options struct {
	// Host is the SNI name and the name verified against the certificate.
	Host string

	// RootCAsDER are additional trust anchors, DER encoded. When empty the
	// Windows certificate store is used.
	RootCAsDER [][]byte

	// RootCAsOnly ignores the Windows certificate store, trusting only
	// RootCAsDER. With no anchors set this trusts nothing, which is the intent.
	RootCAsOnly bool

	SkipVerify bool

	// MinVersion is the lowest acceptable protocol version, as a wire value.
	// Zero means TLS 1.2.
	MinVersion uint16

	// ClientCertDER and ClientKeyDER are an optional client certificate for
	// mutual TLS. ClientKeyPKCS8 says the key is a PKCS#8 PrivateKeyInfo
	// rather than a bare PKCS#1 RSAPrivateKey.
	ClientCertDER  []byte
	ClientKeyDER   []byte
	ClientKeyPKCS8 bool
}

// Session is a completed TLS session over a caller-owned socket.
// It is not safe for concurrent use.
type Session struct {
	handle C.uintptr_t
	cert   C.uintptr_t
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

// Error carries the class and the raw SECURITY_STATUS or HRESULT.
type Error struct {
	Class  Class
	Status int
}

func (e *Error) Error() string {
	return fmt.Sprintf("schannel: class %d, status 0x%08x", e.Class, uint32(e.Status))
}

// Timeout reports whether this was a deadline expiry.
func (e *Error) Timeout() bool { return e.Class == ClassTimeout }

// StatusKeyUnsupported is reported when a client certificate carries a private
// key this package cannot import. Only RSA keys are supported; Schannel wants a
// CNG key handle, and building one for an EC key means assembling a
// BCRYPT_ECCPRIVATE_BLOB by hand.
//
// Status codes are unsigned here because an HRESULT does not fit in a positive
// int32 constant, and comparing them as uint32 keeps them readable as the hex
// values the Windows headers use.
const StatusKeyUnsupported uint32 = 0x80090302 // SEC_E_UNSUPPORTED_FUNCTION

// Handshake performs the TLS handshake over an already connected socket. The
// socket stays owned by the caller; Close does not close it.
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
	minVersion := opt.MinVersion
	if minVersion == 0 {
		minVersion = ProtocolTLS12
	}

	// The store is created even when empty whenever RootCAsOnly is set: a NULL
	// exclusive root would restore the system defaults, so an empty store is
	// what actually trusts nothing.
	var calist C.uintptr_t
	if len(opt.RootCAsDER) > 0 || caOnly == 1 {
		calist = C.https_sc_calist_new()
		if calist == 0 {
			return nil, &Error{Class: ClassAlloc}
		}
		for _, der := range opt.RootCAsDER {
			if len(der) == 0 {
				continue
			}
			if rc := C.https_sc_calist_add(calist, unsafe.Pointer(&der[0]), C.int(len(der))); rc != C.HTTPS_SC_OK {
				C.https_sc_calist_free(calist)
				return nil, &Error{Class: ClassCA}
			}
		}
	}

	var cert C.uintptr_t
	if len(opt.ClientCertDER) > 0 && len(opt.ClientKeyDER) > 0 {
		pkcs8 := 0
		if opt.ClientKeyPKCS8 {
			pkcs8 = 1
		}
		var status C.int
		rc := C.https_sc_clientcert_new(
			unsafe.Pointer(&opt.ClientCertDER[0]), C.int(len(opt.ClientCertDER)),
			unsafe.Pointer(&opt.ClientKeyDER[0]), C.int(len(opt.ClientKeyDER)),
			C.int(pkcs8), &cert, &status)
		if rc != C.HTTPS_SC_OK {
			if calist != 0 {
				C.https_sc_calist_free(calist)
			}
			return nil, &Error{Class: Class(-int(rc)), Status: int(status)}
		}
	}

	var handle C.uintptr_t
	var status C.int
	rc := C.https_sc_handshake(C.uintptr_t(uintptr(fd)), host, C.int(skip),
		calist, C.int(caOnly), C.int(minVersion), cert,
		C.int64_t(timeoutNanos), &handle, &status)
	if rc != C.HTTPS_SC_OK {
		if calist != 0 {
			C.https_sc_calist_free(calist)
		}
		if cert != 0 {
			C.https_sc_clientcert_free(cert)
		}
		return nil, &Error{Class: Class(-int(rc)), Status: int(status)}
	}
	return &Session{handle: handle, cert: cert}, nil
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
	rc := C.https_sc_read(s.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(timeoutNanos), &n, &status)
	if rc != C.HTTPS_SC_OK {
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
	rc := C.https_sc_write(s.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(timeoutNanos), &n, &status)
	if rc != C.HTTPS_SC_OK {
		return int(n), &Error{Class: Class(-int(rc)), Status: int(status)}
	}
	return int(n), nil
}

// Close releases the session. It does not close the caller's socket.
func (s *Session) Close() {
	if s == nil {
		return
	}
	if s.handle != 0 {
		C.https_sc_close(s.handle)
		s.handle = 0
	}
	if s.cert != 0 {
		C.https_sc_clientcert_free(s.cert)
		s.cert = 0
	}
}
