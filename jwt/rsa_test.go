//go:build !tinygo && !jwt_no_rsa

package jwt

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

func TestRS256Verification(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	signingInput := encode([]byte(`{"alg":"RS256","kid":"rsa"}`)) + "." + encode([]byte(`{"exp":4102444800}`))
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	token, err := Parse(signingInput+"."+encode(signature), ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(token, KeyResolverFunc(func(Header) (VerificationKey, error) {
		return VerificationKey{Algorithm: "RS256", RSA: &privateKey.PublicKey}, nil
	}), VerifyOptions{AllowedAlgorithms: []string{"RS256"}, Clock: func() time.Time { return time.Unix(1_700_000_000, 0) }})
	if err != nil {
		t.Fatal(err)
	}
}
