package google

import (
	"crypto/sha256"

	"github.com/shibukawa/tinygodriver/internal/rsasign"
	"github.com/shibukawa/tinygodriver/jwt"
)

// RSASigner signs JWTs with a service account key. It implements jwt.Signer,
// which is the seam that keeps the jwt package free of build tags and cgo: the
// RSA operation happens in internal/rsasign, which is crypto/rsa on host Go and
// the OS crypto library on TinyGo builds.
//
// It holds a native key handle on some builds, so Close is not optional.
type RSASigner struct {
	key *rsasign.Key
}

// NewRSASigner parses the PKCS#8 private key in c.
func NewRSASigner(c Credentials) (*RSASigner, error) {
	if !c.Valid() {
		return nil, ErrNoCredentials
	}
	key, err := rsasign.ParsePKCS8([]byte(c.PrivateKey))
	if err != nil {
		return nil, ErrBadPrivateKey
	}
	return &RSASigner{key: key}, nil
}

// Algorithm reports RS256, the only algorithm Google service account keys use.
func (s *RSASigner) Algorithm() string { return "RS256" }

// Sign hashes the JWS signing input and signs the digest.
func (s *RSASigner) Sign(signingInput []byte) ([]byte, error) {
	if s == nil || s.key == nil {
		return nil, ErrNoCredentials
	}
	digest := sha256.Sum256(signingInput)
	return s.key.SignPKCS1v15SHA256(digest[:])
}

// Close releases the key. It is safe to call more than once.
func (s *RSASigner) Close() error {
	if s == nil || s.key == nil {
		return nil
	}
	return s.key.Close()
}

// SignerBackend names the RSA implementation this build selected. Backend,
// without the prefix, is the HTTP stack, matching cloud/aws.
func SignerBackend() string { return rsasign.Backend }

// compile-time proof that the seam fits.
var _ jwt.Signer = (*RSASigner)(nil)
