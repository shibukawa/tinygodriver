//go:build jwt_no_rsa

package jwt

import (
	"errors"
	"testing"
)

// The jwt_no_rsa build must keep HS256 whole while refusing RS256, rather
// than mis-verifying or panicking on it.

func TestNoRSABuildRefusesRS256(t *testing.T) {
	token := &Token{
		Header:       Header{Algorithm: "RS256"},
		signingInput: "a.b",
		Signature:    []byte{1},
	}
	resolver := KeyResolverFunc(func(Header) (VerificationKey, error) {
		return VerificationKey{Algorithm: "RS256"}, nil
	})
	_, err := Verify(token, resolver, VerifyOptions{
		AllowedAlgorithms:      []string{"RS256"},
		AllowMissingExpiration: true,
	})
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("err = %v, want ErrUnsupportedAlgorithm", err)
	}
}

func TestNoRSABuildKeepsHS256(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	signer, err := NewHMACSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := Sign(Header{Type: "JWT"}, Claims{Issuer: "iss"}, signer)
	if err != nil {
		t.Fatal(err)
	}
	resolver := KeyResolverFunc(func(Header) (VerificationKey, error) {
		return VerificationKey{Algorithm: "HS256", HMAC: key}, nil
	})
	claims, err := ParseAndVerify(compact, resolver, ParseOptions{}, VerifyOptions{
		AllowedAlgorithms:      []string{"HS256"},
		AllowMissingExpiration: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != "iss" {
		t.Fatalf("issuer = %q", claims.Issuer)
	}
}
