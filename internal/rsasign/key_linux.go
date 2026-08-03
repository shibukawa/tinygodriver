//go:build (tinygo || force_tinygo_logic) && linux

package rsasign

import "github.com/shibukawa/tinygodriver/internal/mbedtls"

// Backend identifies the RSA implementation selected by build constraints.
const Backend = "mbedtls"

// Key wraps the mbedTLS key. This backend adds no object code: every module it
// needs is already enabled in the vendored mbedtls_config.h for TLS, so linux
// is the one platform where the native path is strictly less work than the
// pure-Go one.
type Key struct {
	key *mbedtls.PrivateKey
}

// parsePKCS8DER hands the container straight over. mbedTLS parses PKCS#8
// itself, so unlike the darwin backend there is no DER walker on this path —
// the walker still runs, to reject a malformed key identically on every
// platform, but its output is not what gets signed with.
func parsePKCS8DER(der []byte) (*Key, error) {
	pkcs1, err := pkcs1FromPKCS8(der)
	if err != nil {
		return nil, err
	}
	if _, err := rsaModulusBits(pkcs1); err != nil {
		return nil, err
	}
	key, err := mbedtls.ParsePrivateKey(der)
	if err != nil {
		return nil, ErrBadKey
	}
	return &Key{key: key}, nil
}

// SignPKCS1v15SHA256 signs a 32-byte SHA-256 digest.
func (k *Key) SignPKCS1v15SHA256(digest []byte) ([]byte, error) {
	if k == nil || k.key == nil {
		return nil, ErrClosed
	}
	if len(digest) != sha256Size {
		return nil, ErrBadDigest
	}
	sig, err := k.key.SignPKCS1v15SHA256(digest)
	if err != nil {
		return nil, ErrSignFailed
	}
	return sig, nil
}

// Bits is the modulus size, which is also the signature length in bits.
func (k *Key) Bits() int {
	if k == nil {
		return 0
	}
	return k.key.Bits()
}

// Close releases the native key. It is safe to call more than once.
func (k *Key) Close() error {
	if k == nil || k.key == nil {
		return nil
	}
	err := k.key.Close()
	k.key = nil
	return err
}
