// Package authn contains bounded security primitives shared by contrib
// authentication protocols. Protocol-specific algorithm and key policy does
// not belong in this package.
package authn

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"time"
)

var (
	ErrInvalidSize     = errors.New("authn: invalid size")
	ErrInvalidEncoding = errors.New("authn: invalid encoding")
	ErrLimitExceeded   = errors.New("authn: limit exceeded")
	ErrExpired         = errors.New("authn: value expired")
	ErrInvalidVerifier = errors.New("authn: invalid PKCE verifier")
)

const MaxSecretBytes = 1024

// MaxEncodedSecretBytes is the largest unpadded Base64url representation of
// MaxSecretBytes.
const MaxEncodedSecretBytes = ((MaxSecretBytes + 2) / 3) * 4

// GenerateSecret returns an unpadded Base64url value containing exactly
// byteCount bytes of cryptographic randomness.
func GenerateSecret(random io.Reader, byteCount int) (string, error) {
	if byteCount <= 0 || byteCount > MaxSecretBytes {
		return "", ErrInvalidSize
	}
	if random == nil {
		random = rand.Reader
	}
	raw := make([]byte, byteCount)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// EqualSecret compares two bounded secrets without leaking their contents.
// Authentication protocols should generate fixed-length values.
func EqualSecret(left, right string) bool {
	if len(left) > MaxEncodedSecretBytes || len(right) > MaxEncodedSecretBytes {
		return false
	}
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	equalHash := subtle.ConstantTimeCompare(leftHash[:], rightHash[:])
	equalLength := subtle.ConstantTimeEq(int32(len(left)), int32(len(right)))
	return equalHash&equalLength == 1
}

// DecodeBase64URL strictly decodes canonical, unpadded Base64url.
func DecodeBase64URL(value string, maxEncodedBytes, maxDecodedBytes int) ([]byte, error) {
	if maxEncodedBytes <= 0 || maxDecodedBytes <= 0 {
		return nil, ErrInvalidSize
	}
	if len(value) > maxEncodedBytes || base64.RawURLEncoding.DecodedLen(len(value)) > maxDecodedBytes {
		return nil, ErrLimitExceeded
	}
	if strings.ContainsRune(value, '=') {
		return nil, ErrInvalidEncoding
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, ErrInvalidEncoding
	}
	return decoded, nil
}

func RequireUnexpired(now, expiresAt time.Time) error {
	if expiresAt.IsZero() || !expiresAt.After(now) {
		return ErrExpired
	}
	return nil
}

// GeneratePKCEVerifier returns a 43-character verifier with 256 random bits.
func GeneratePKCEVerifier(random io.Reader) (string, error) {
	return GenerateSecret(random, 32)
}

func ValidatePKCEVerifier(verifier string) error {
	if len(verifier) < 43 || len(verifier) > 128 {
		return ErrInvalidVerifier
	}
	for _, char := range verifier {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '.' || char == '_' || char == '~' {
			continue
		}
		return ErrInvalidVerifier
	}
	return nil
}

func PKCEChallengeS256(verifier string) (string, error) {
	if err := ValidatePKCEVerifier(verifier); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:]), nil
}
