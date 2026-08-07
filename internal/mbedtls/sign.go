//go:build (tinygo || force_tinygo_logic) && ((linux && !wasip2) || (darwin && darwinstarttlswith13))

package mbedtls

/*
#include <stdlib.h>
#include "rsasign_mbedtls.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"unsafe"
)

// Signing failures. They are deliberately coarse: mbedTLS reports a status
// code, but nothing above this layer can act on the difference between one
// parse failure and another.
var (
	ErrSignKeyParse = errors.New("mbedtls: not a usable RSA private key")
	ErrSignFailed   = errors.New("mbedtls: signing failed")
	ErrSignClosed   = errors.New("mbedtls: key is closed")
)

// PrivateKey is an RSA private key held by mbedTLS, together with the DRBG it
// draws blinding material from.
//
// It exists so internal/rsasign has a linux backend without compiling a second
// copy of mbedTLS: C symbols are global, so two packages building the same
// sources would collide at link time. That is also why this package moved out
// from under https/ — Go's internal rule made it unreachable from anywhere
// else, and sharing one build is the only correct answer.
type PrivateKey struct {
	handle unsafe.Pointer
	bits   int
}

// ParsePrivateKey reads a PKCS#8 private key.
//
// The input may be DER or PEM; mbedTLS sniffs one against the other, so unlike
// the Security.framework backend nothing has to unwrap the container first. A
// PEM input must carry its terminating NUL inside der.
func ParsePrivateKey(der []byte) (*PrivateKey, error) {
	if len(der) == 0 {
		return nil, ErrSignKeyParse
	}
	// C must not retain a Go pointer past the call, so the key crosses in C
	// memory and is freed here.
	buf := C.CBytes(der)
	defer C.free(buf)

	var handle unsafe.Pointer
	if rc := C.mbedtls_rsasign_load((*C.uchar)(buf), C.size_t(len(der)), &handle); rc != C.MBED_RSASIGN_OK {
		return nil, ErrSignKeyParse
	}
	bits := int(C.mbedtls_rsasign_bits(handle))
	if bits <= 0 {
		C.mbedtls_rsasign_free(handle)
		return nil, ErrSignKeyParse
	}
	k := &PrivateKey{handle: handle, bits: bits}
	// A dropped key would otherwise leak its contexts for the life of the
	// process. Close is still the documented obligation; this bounds the damage
	// when a caller forgets.
	runtime.SetFinalizer(k, func(k *PrivateKey) { _ = k.Close() })
	return k, nil
}

// SignPKCS1v15SHA256 signs a 32-byte SHA-256 digest.
func (k *PrivateKey) SignPKCS1v15SHA256(digest []byte) ([]byte, error) {
	if k == nil || k.handle == nil {
		return nil, ErrSignClosed
	}
	cd := C.CBytes(digest)
	defer C.free(cd)

	out := make([]byte, k.bits/8)
	var n C.size_t
	rc := C.mbedtls_rsasign_sign(k.handle,
		(*C.uchar)(cd), C.size_t(len(digest)),
		(*C.uchar)(unsafe.Pointer(&out[0])), C.size_t(len(out)), &n)
	runtime.KeepAlive(out)
	if rc != C.MBED_RSASIGN_OK {
		return nil, ErrSignFailed
	}
	return out[:int(n)], nil
}

// Bits is the modulus size, which is also the signature length in bits.
func (k *PrivateKey) Bits() int {
	if k == nil {
		return 0
	}
	return k.bits
}

// Close releases the key and its DRBG. It is safe to call more than once.
func (k *PrivateKey) Close() error {
	if k == nil || k.handle == nil {
		return nil
	}
	C.mbedtls_rsasign_free(k.handle)
	k.handle = nil
	runtime.SetFinalizer(k, nil)
	return nil
}
