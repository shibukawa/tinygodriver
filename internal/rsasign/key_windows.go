//go:build (tinygo || force_tinygo_logic) && windows

package rsasign

/*
#include <stdlib.h>
#include "rsasign.h"
*/
import "C"

import (
	"runtime"
	"unsafe"
)

// Backend identifies the RSA implementation selected by build constraints.
const Backend = "cng"

// Key holds a CNG key handle and the algorithm provider it came from. Both are
// released by exactly one path, Close.
type Key struct {
	handle unsafe.Pointer
	bits   int
}

// parsePKCS8DER unwraps the PKCS#8 container in Go and lets crypt32 turn the
// PKCS#1 structure into the key blob CNG imports. Doing the second step here
// would mean decoding the RSA components by hand, which is what the walker in
// pkcs8.go must not become.
func parsePKCS8DER(der []byte) (*Key, error) {
	pkcs1, err := pkcs1FromPKCS8(der)
	if err != nil {
		return nil, err
	}
	bits, err := rsaModulusBits(pkcs1)
	if err != nil {
		return nil, err
	}
	buf := C.CBytes(pkcs1)
	defer C.free(buf)

	var handle unsafe.Pointer
	if rc := C.rsasign_load((*C.uchar)(buf), C.size_t(len(pkcs1)), &handle); rc != C.RSASIGN_OK {
		return nil, ErrBadKey
	}
	k := &Key{handle: handle, bits: bits}
	runtime.SetFinalizer(k, func(k *Key) { _ = k.Close() })
	return k, nil
}

// SignPKCS1v15SHA256 signs a 32-byte SHA-256 digest.
func (k *Key) SignPKCS1v15SHA256(digest []byte) ([]byte, error) {
	if k == nil || k.handle == nil {
		return nil, ErrClosed
	}
	if len(digest) != sha256Size {
		return nil, ErrBadDigest
	}
	cd := C.CBytes(digest)
	defer C.free(cd)

	out := make([]byte, k.bits/8)
	var n C.size_t
	rc := C.rsasign_sign(k.handle,
		(*C.uchar)(cd), C.size_t(len(digest)),
		(*C.uchar)(unsafe.Pointer(&out[0])), C.size_t(len(out)), &n)
	runtime.KeepAlive(out)
	if rc != C.RSASIGN_OK {
		return nil, ErrSignFailed
	}
	return out[:int(n)], nil
}

// Bits is the modulus size, which is also the signature length in bits.
func (k *Key) Bits() int {
	if k == nil {
		return 0
	}
	return k.bits
}

// Close releases the key handle and its algorithm provider. It is safe to call
// more than once.
func (k *Key) Close() error {
	if k == nil || k.handle == nil {
		return nil
	}
	C.rsasign_free(k.handle)
	k.handle = nil
	runtime.SetFinalizer(k, nil)
	return nil
}
