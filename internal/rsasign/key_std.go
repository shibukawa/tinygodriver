//go:build !tinygo && !force_tinygo_logic

package rsasign

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
)

// Backend identifies the RSA implementation selected by build constraints.
const Backend = "crypto/rsa"

// Key is a private key ready to sign. On this build it holds a parsed
// *rsa.PrivateKey; there is no native handle and Close is a formality kept so
// callers need not know which build they are on.
type Key struct {
	key  *rsa.PrivateKey
	bits int
}

func parsePKCS8DER(der []byte) (*Key, error) {
	// The bit-length check runs on both paths so a rejected key is rejected
	// identically, rather than only where the backend happens to object.
	pkcs1, err := pkcs1FromPKCS8(der)
	if err != nil {
		return nil, err
	}
	bits, err := rsaModulusBits(pkcs1)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, ErrBadKey
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, ErrBadKey
	}
	return &Key{key: key, bits: bits}, nil
}

// SignPKCS1v15SHA256 signs a 32-byte SHA-256 digest.
func (k *Key) SignPKCS1v15SHA256(digest []byte) ([]byte, error) {
	if k == nil || k.key == nil {
		return nil, ErrClosed
	}
	if len(digest) != sha256Size {
		return nil, ErrBadDigest
	}
	return rsa.SignPKCS1v15(rand.Reader, k.key, crypto.SHA256, digest)
}

// Bits is the modulus size, which is also the signature length in bits.
func (k *Key) Bits() int {
	if k == nil {
		return 0
	}
	return k.bits
}

// Close releases the key. It is safe to call more than once.
func (k *Key) Close() error {
	if k == nil {
		return nil
	}
	k.key = nil
	return nil
}
