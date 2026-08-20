//go:build jwt_no_rsa

package jwt

import "crypto/rsa"

// signingAlgorithms excludes RS256: this build cannot verify what an RS256
// signer would emit, and the allowlist invariant says Sign must not produce a
// token the package cannot itself check.
var signingAlgorithms = [...]string{"HS256"}

// verifyRS256 under the jwt_no_rsa tag: RS256 tokens are refused, and the
// binary carries neither crypto/rsa's arithmetic nor math/big. The parameter
// keeps the *rsa.PublicKey type so VerificationKey stays the same shape on
// both builds; a type reference alone links none of the RSA code.
func verifyRS256([]byte, *rsa.PublicKey, []byte) error {
	return ErrUnsupportedAlgorithm
}
