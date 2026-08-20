//go:build !tinygo && !jwt_no_rsa

package jwt

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"testing"
	"time"
)

// Parse plus Verify is the whole hot path of a service checking bearer tokens,
// so the two algorithms are measured end to end: HS256 is dominated by the
// parser, RS256 by the RSA exponentiation, and the parser's share should be
// visible against both.

var benchHMACKey = []byte("0123456789abcdef0123456789abcdef")

var benchClock = func() time.Time { return time.Unix(1_700_000_000, 0) }

func benchVerifyOptions(algorithm string) VerifyOptions {
	return VerifyOptions{AllowedAlgorithms: []string{algorithm}, Clock: benchClock}
}

func benchHS256Token(b *testing.B) string {
	signer, err := NewHMACSigner(benchHMACKey)
	if err != nil {
		b.Fatal(err)
	}
	exp := int64(4102444800)
	compact, err := Sign(Header{KeyID: "hmac"}, Claims{
		Issuer: "https://issuer.example", Subject: "device-42",
		Audience: []string{"https://api.example"}, ExpiresAt: &exp,
	}, signer)
	if err != nil {
		b.Fatal(err)
	}
	return compact
}

func BenchmarkParseVerifyHS256(b *testing.B) {
	compact := benchHS256Token(b)
	resolver := KeyResolverFunc(func(Header) (VerificationKey, error) {
		return VerificationKey{Algorithm: "HS256", HMAC: benchHMACKey}, nil
	})
	options := benchVerifyOptions("HS256")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseAndVerify(compact, resolver, ParseOptions{}, options); err != nil {
			b.Fatal(err)
		}
	}
}

// The RSA key is generated once. Generation is slow and outside what these
// benchmarks measure; the fixed inputs are the token bytes, which are built
// from constants.
var benchRSA struct {
	once sync.Once
	key  *rsa.PrivateKey
	err  error
}

func benchRS256Token(b *testing.B) (string, *rsa.PublicKey) {
	benchRSA.once.Do(func() {
		benchRSA.key, benchRSA.err = rsa.GenerateKey(rand.Reader, 2048)
	})
	if benchRSA.err != nil {
		b.Fatal(benchRSA.err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	signingInput := encode([]byte(`{"alg":"RS256","kid":"rsa"}`)) + "." +
		encode([]byte(`{"iss":"https://issuer.example","sub":"device-42","aud":"https://api.example","exp":4102444800}`))
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, benchRSA.key, crypto.SHA256, digest[:])
	if err != nil {
		b.Fatal(err)
	}
	return signingInput + "." + encode(signature), &benchRSA.key.PublicKey
}

func BenchmarkParseVerifyRS256(b *testing.B) {
	compact, publicKey := benchRS256Token(b)
	resolver := KeyResolverFunc(func(Header) (VerificationKey, error) {
		return VerificationKey{Algorithm: "RS256", RSA: publicKey}, nil
	})
	options := benchVerifyOptions("RS256")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseAndVerify(compact, resolver, ParseOptions{}, options); err != nil {
			b.Fatal(err)
		}
	}
}

// ResolveKey through a parsed JWKS, separated from the verification so the
// cost of resolving is visible on its own; it runs once per verification in a
// service that keys by kid.
func BenchmarkJWKSResolveRS256(b *testing.B) {
	_, publicKey := benchRS256Token(b)
	encode := base64.RawURLEncoding.EncodeToString
	exponent := []byte{byte(publicKey.E >> 16), byte(publicKey.E >> 8), byte(publicKey.E)}
	document := []byte(`{"keys":[` +
		`{"kty":"oct","kid":"hmac","alg":"HS256","k":"` + encode(benchHMACKey) + `"},` +
		`{"kty":"RSA","kid":"rsa","alg":"RS256","n":"` + encode(publicKey.N.Bytes()) + `","e":"` + encode(exponent) + `"}]}`)
	set, err := ParseJWKS(document, JWKSOptions{})
	if err != nil {
		b.Fatal(err)
	}
	header := Header{Algorithm: "RS256", KeyID: "rsa"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := set.ResolveKey(header); err != nil {
			b.Fatal(err)
		}
	}
}
