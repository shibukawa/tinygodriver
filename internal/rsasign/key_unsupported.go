//go:build (tinygo || force_tinygo_logic) && !darwin && !linux && !windows

package rsasign

// Backend identifies the RSA implementation selected by build constraints.
const Backend = "unsupported"

// Key exists so the package compiles everywhere; no build reaching this file
// can sign.
//
// linux is next and cheapest: the vendored mbedTLS already enables
// MBEDTLS_RSA_C, PKCS1_V15, PK_C, PK_PARSE_C and ASN1_PARSE_C for TLS, so
// mbedtls_pk_parse_key and mbedtls_pk_sign add no object code. windows needs
// CNG with crypt32 converting the key blob, and cannot be run here at all.
//
// This file never falls back to crypto/rsa. A fallback would satisfy the
// no-crypto/rsa property on the builds that were checked and break it on the
// ones that were not.
type Key struct{}

func parsePKCS8DER([]byte) (*Key, error) { return nil, ErrPlatformNotSupported }

// SignPKCS1v15SHA256 always fails on this build.
func (k *Key) SignPKCS1v15SHA256([]byte) ([]byte, error) { return nil, ErrPlatformNotSupported }

// Bits always reports zero on this build.
func (k *Key) Bits() int { return 0 }

// Close is a no-op on this build.
func (k *Key) Close() error { return nil }
