//go:build !jwt_no_rsa

package jwt

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
)

// signingAlgorithms carries RS256 only on builds that can also verify it; see
// the comment in sign.go.
var signingAlgorithms = [...]string{"HS256", "RS256"}

// verifyRS256 checks an RS256 signature. It is the one call that makes
// crypto/rsa and math/big reachable from Verify, which is why it sits behind
// the jwt_no_rsa build tag: a program that only ever verifies HS256 can build
// with -tags jwt_no_rsa and leave the whole RSA stack out of the binary.
func verifyRS256(signingInput string, key *rsa.PublicKey, signature []byte) error {
	if key == nil || key.N == nil || key.N.BitLen() < 2048 || key.E < 3 {
		return ErrKeyNotFound
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return ErrInvalidSignature
	}
	return nil
}
