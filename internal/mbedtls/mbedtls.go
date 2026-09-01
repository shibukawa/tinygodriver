//go:build (tinygo || force_tinygo_logic) && ((linux && !wasip2) || (darwin && darwinstarttlswith13))

package mbedtls

/*
#include <stdlib.h>
#include "tls_mbedtls.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"unsafe"
)

// Supported reports whether this build has the mbedTLS backend compiled in.
const Supported = true

// Options configures one handshake. All PEM fields are raw PEM bytes; this
// package appends the terminating NUL that mbedtls_x509_crt_parse requires.
type Options struct {
	// Host is the SNI name and the name verified against the certificate.
	Host string

	// RootCAsPEM holds the trust anchors, concatenated. Ignored when
	// SkipVerify is set.
	RootCAsPEM []byte

	// ClientCertPEM and ClientKeyPEM enable mutual TLS when both are set.
	ClientCertPEM []byte
	ClientKeyPEM  []byte

	SkipVerify bool

	// MinVersion is the wire version, e.g. 0x0303 for TLS 1.2. Zero leaves the
	// mbedTLS default.
	MinVersion uint16
}

// Session is a completed TLS session over a caller-owned file descriptor.
// It is not safe for concurrent use.
type Session struct {
	handle C.uintptr_t
}

// nulTerminated returns a copy with a trailing NUL, which mbedTLS counts as
// part of the length for PEM input.
func nulTerminated(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	if b[len(b)-1] == 0 {
		return b
	}
	out := make([]byte, len(b)+1)
	copy(out, b)
	return out
}

// ptrLen yields a C pointer and length for a byte slice, or nil and 0.
// The slice must stay alive across the call; callers use runtime.KeepAlive.
func ptrLen(b []byte) (unsafe.Pointer, C.int) {
	if len(b) == 0 {
		return nil, 0
	}
	return unsafe.Pointer(&b[0]), C.int(len(b))
}

// Handshake performs the TLS handshake over an already connected socket.
// The file descriptor stays owned by the caller; Close does not close it.
func Handshake(fd int, opt Options, timeoutNanos int64) (*Session, *Error) {
	host := C.CString(opt.Host)
	defer C.free(unsafe.Pointer(host))

	ca := nulTerminated(opt.RootCAsPEM)
	cert := nulTerminated(opt.ClientCertPEM)
	key := nulTerminated(opt.ClientKeyPEM)

	caPtr, caLen := ptrLen(ca)
	certPtr, certLen := ptrLen(cert)
	keyPtr, keyLen := ptrLen(key)

	skip := C.int(0)
	if opt.SkipVerify {
		skip = 1
	}

	var handle C.uintptr_t
	var tlsErr C.int
	var flags C.uint

	rc := C.https_mbed_handshake(C.int(fd), host, skip, C.int(opt.MinVersion),
		(*C.uchar)(caPtr), caLen,
		(*C.uchar)(certPtr), certLen,
		(*C.uchar)(keyPtr), keyLen,
		C.int64_t(timeoutNanos), &handle, &tlsErr, &flags)

	runtime.KeepAlive(ca)
	runtime.KeepAlive(cert)
	runtime.KeepAlive(key)

	if rc != C.HTTPS_MBED_OK {
		return nil, &Error{
			Class:       Class(-int(rc)),
			Code:        int(tlsErr),
			VerifyFlags: uint32(flags),
		}
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
	var n, tlsErr C.int
	rc := C.https_mbed_read(s.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(timeoutNanos), &n, &tlsErr)
	if rc != C.HTTPS_MBED_OK {
		return 0, &Error{Class: Class(-int(rc)), Code: int(tlsErr)}
	}
	return int(n), nil
}

// Write encrypts p in full, or reports how much made it out.
func (s *Session) Write(p []byte, timeoutNanos int64) (int, *Error) {
	if s.handle == 0 {
		return 0, &Error{Class: ClassClosed}
	}
	if len(p) == 0 {
		return 0, nil
	}
	var n, tlsErr C.int
	rc := C.https_mbed_write(s.handle, unsafe.Pointer(&p[0]), C.int(len(p)),
		C.int64_t(timeoutNanos), &n, &tlsErr)
	if rc != C.HTTPS_MBED_OK {
		return int(n), &Error{Class: Class(-int(rc)), Code: int(tlsErr)}
	}
	return int(n), nil
}

// Close releases the session. It is safe to call more than once, and it does
// not close the caller's file descriptor.
func (s *Session) Close() {
	if s == nil || s.handle == 0 {
		return
	}
	C.https_mbed_close(s.handle)
	s.handle = 0
}

// SelfTest runs the mbedTLS known-answer vectors for AES, GCM, SHA-256 and
// SHA-512. Under TinyGo it is what validates the bundled arm_neon.h, which no
// other test reaches; https/mbedtls_test.go calls it everywhere this package
// builds.
func SelfTest() error {
	if rc := int(C.https_mbed_self_test()); rc != 0 {
		switch rc {
		case -1:
			return errors.New("mbedtls: AES self test failed")
		case -2:
			return errors.New("mbedtls: GCM self test failed")
		case -3:
			return errors.New("mbedtls: SHA-256 self test failed")
		case -4:
			return errors.New("mbedtls: SHA-512 self test failed")
		}
		return errors.New("mbedtls: self test failed")
	}
	return nil
}

// HWCaps reports kernel-detected CPU crypto features: bit 0 AES, bit 1
// SHA-256, bit 2 SHA-512. It returns -1 where the concept does not apply.
func HWCaps() int { return int(C.https_mbed_hwcaps()) }
