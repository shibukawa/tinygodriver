package jwt

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func int64Pointer(value int64) *int64 { return &value }

// unsupportedSigningAlgorithm reported RS256 upstream, where signing was HS256
// only. RS256 signing is supported here, so the fixture moved to an algorithm
// that is genuinely outside the allowlist; ES256 is the natural next request
// and is still unimplemented in both directions.
type unsupportedSigningAlgorithm struct{}

func (unsupportedSigningAlgorithm) Algorithm() string { return "ES256" }
func (unsupportedSigningAlgorithm) Sign([]byte) ([]byte, error) {
	return []byte("signature"), nil
}

// noneSigningAlgorithm is the attack the allowlist exists for.
type noneSigningAlgorithm struct{}

func (noneSigningAlgorithm) Algorithm() string           { return "none" }
func (noneSigningAlgorithm) Sign([]byte) ([]byte, error) { return nil, nil }

func TestNilSigningBoundariesAreSafe(t *testing.T) {
	var signer *HMACSigner
	if _, err := signer.Sign([]byte("input")); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("nil signer = %v", err)
	}
	var resolver KeyResolverFunc
	if _, err := resolver.ResolveKey(Header{}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("nil resolver = %v", err)
	}
}

func TestParseRejectsUnboundedOptions(t *testing.T) {
	for _, options := range []ParseOptions{
		{MaxTokenBytes: maxMaxTokenBytes + 1},
		{MaxSegmentBytes: maxMaxSegmentBytes + 1},
		{MaxJSONDepth: maxMaxJSONDepth + 1},
		{MaxJSONMembers: maxMaxJSONMembers + 1},
	} {
		if _, err := Parse("x", options); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("options %#v error = %v", options, err)
		}
	}
}

func TestSigningRejectsUnboundedInputs(t *testing.T) {
	if _, err := NewHMACSigner(make([]byte, maxSignerKeyBytes+1)); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("oversized signer key error = %v", err)
	}
	signer, err := NewHMACSigner(bytes.Repeat([]byte{'k'}, 32))
	if err != nil {
		t.Fatal(err)
	}
	claims := Claims{Raw: map[string]json.RawMessage{
		"large": json.RawMessage(`"` + strings.Repeat("x", maxMaxSegmentBytes) + `"`),
	}}
	if _, err := Sign(Header{}, claims, signer); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized claims error = %v", err)
	}
}

func TestSigningRejectsUnsupportedCustomAlgorithm(t *testing.T) {
	if _, err := Sign(Header{Algorithm: "ES256"}, Claims{}, unsupportedSigningAlgorithm{}); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("custom algorithm error = %v", err)
	}
	if _, err := Sign(Header{Algorithm: "none"}, Claims{}, noneSigningAlgorithm{}); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("alg=none error = %v", err)
	}
}

// TestSigningAllowlistMatchesVerifier pins the invariant the allowlist is for:
// this package must not emit a token it cannot itself check.
func TestSigningAllowlistMatchesVerifier(t *testing.T) {
	for _, algorithm := range signingAlgorithms {
		if !canSign(algorithm) {
			t.Errorf("%s is in the list but canSign says no", algorithm)
		}
		// verifySignature's default branch is the verifier's own allowlist.
		err := verifySignature(&Token{}, VerificationKey{Algorithm: algorithm})
		if errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Errorf("Sign emits %s but verifySignature does not accept it", algorithm)
		}
	}
	if canSign("ES256") || canSign("none") || canSign("") {
		t.Error("allowlist admits an algorithm the verifier rejects")
	}
}

func TestHS256SignParseAndVerify(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, err := NewHMACSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	compact, err := Sign(Header{Type: "JWT", KeyID: "main"}, Claims{
		Issuer: "issuer", Subject: "subject", Audience: []string{"client"},
		ExpiresAt: int64Pointer(now.Add(time.Minute).Unix()), IssuedAt: int64Pointer(now.Unix()),
		Raw: map[string]json.RawMessage{"role": json.RawMessage(`"admin"`)},
	}, signer)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseAndVerify(compact, KeyResolverFunc(func(header Header) (VerificationKey, error) {
		if header.KeyID != "main" {
			return VerificationKey{}, ErrKeyNotFound
		}
		return VerificationKey{Algorithm: "HS256", HMAC: key}, nil
	}), ParseOptions{}, VerifyOptions{
		AllowedAlgorithms: []string{"HS256"}, Issuer: "issuer", Audience: "client",
		TokenType: "JWT", Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "subject" {
		t.Fatalf("subject = %q", claims.Subject)
	}
	if role, ok := claims.String("role"); !ok || role != "admin" {
		t.Fatalf("role = %q, %v", role, ok)
	}
	if raw, ok := claims.Value("role"); !ok || string(raw) != `"admin"` {
		t.Fatalf("raw role = %s, %v", raw, ok)
	}
	if _, ok := claims.Value("missing"); ok {
		t.Fatal("missing claim unexpectedly present")
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, err := NewHMACSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := Sign(Header{}, Claims{Issuer: "issuer"}, signer)
	if err != nil {
		t.Fatal(err)
	}
	segments := strings.Split(compact, ".")
	if len(segments) != 3 {
		t.Fatalf("compact segments = %d", len(segments))
	}
	last := segments[2][len(segments[2])-1]
	if last == 'A' {
		last = 'B'
	} else {
		last = 'A'
	}
	segments[2] = segments[2][:len(segments[2])-1] + string(last)
	token, err := Parse(strings.Join(segments, "."), ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(token, KeyResolverFunc(func(Header) (VerificationKey, error) {
		return VerificationKey{Algorithm: "HS256", HMAC: key}, nil
	}), VerifyOptions{AllowedAlgorithms: []string{"HS256"}})
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered signature error = %v", err)
	}
}

func TestParseRejectsDuplicateAndNonCanonicalSegments(t *testing.T) {
	encode := base64.RawURLEncoding.EncodeToString
	duplicate := encode([]byte(`{"alg":"HS256","alg":"RS256"}`)) + "." + encode([]byte(`{"exp":1}`)) + ".AA"
	if _, err := Parse(duplicate, ParseOptions{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("duplicate error = %v", err)
	}
	padded := encode([]byte(`{"alg":"HS256"}`)) + "=." + encode([]byte(`{"exp":1}`)) + ".AA"
	if _, err := Parse(padded, ParseOptions{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("padding error = %v", err)
	}
}

func TestParseRejectsInvalidAudienceValues(t *testing.T) {
	encode := base64.RawURLEncoding.EncodeToString
	for _, audience := range []string{`{"aud":""}`, `{"aud":[]}`, `{"aud":["client",""]}`} {
		compact := encode([]byte(`{"alg":"HS256"}`)) + "." + encode([]byte(audience)) + ".AA"
		if _, err := Parse(compact, ParseOptions{}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("audience %s error = %v", audience, err)
		}
	}
	tooMany := make([]string, 65)
	for i := range tooMany {
		tooMany[i] = "client"
	}
	claims, _ := json.Marshal(map[string]any{"aud": tooMany})
	compact := encode([]byte(`{"alg":"HS256"}`)) + "." + encode(claims) + ".AA"
	if _, err := Parse(compact, ParseOptions{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("too many audiences error = %v", err)
	}
}

func TestVerifyRejectsNoneAndAlgorithmConfusion(t *testing.T) {
	encode := base64.RawURLEncoding.EncodeToString
	none := encode([]byte(`{"alg":"none"}`)) + "." + encode([]byte(`{"exp":4102444800}`)) + ".AA"
	token, err := Parse(none, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(token, KeyResolverFunc(func(Header) (VerificationKey, error) {
		return VerificationKey{Algorithm: "none"}, nil
	}), VerifyOptions{AllowedAlgorithms: []string{"none"}})
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("none error = %v", err)
	}

	key := []byte("0123456789abcdef0123456789abcdef")
	signer, _ := NewHMACSigner(key)
	compact, err := Sign(Header{KeyID: "key"}, Claims{ExpiresAt: int64Pointer(4102444800)}, signer)
	if err != nil {
		t.Fatal(err)
	}
	token, err = Parse(compact, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(token, KeyResolverFunc(func(Header) (VerificationKey, error) {
		return VerificationKey{Algorithm: "RS256", HMAC: key}, nil
	}), VerifyOptions{AllowedAlgorithms: []string{"HS256"}})
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("confusion error = %v", err)
	}
}

func TestVerifyClaimsPolicy(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, _ := NewHMACSigner(key)
	now := time.Unix(1_700_000_000, 0)
	compact, err := Sign(Header{}, Claims{ExpiresAt: int64Pointer(now.Unix())}, signer)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := Parse(compact, ParseOptions{})
	_, err = Verify(token, KeyResolverFunc(func(Header) (VerificationKey, error) {
		return VerificationKey{Algorithm: "HS256", HMAC: key}, nil
	}), VerifyOptions{AllowedAlgorithms: []string{"HS256"}, Clock: func() time.Time { return now }})
	if !errors.Is(err, ErrInvalidClaims) || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expiration error = %v", err)
	}
}

func TestJWKSSelectionAndAmbiguity(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	encodedKey := base64.RawURLEncoding.EncodeToString(key)
	document := []byte(`{"keys":[{"kty":"oct","kid":"one","use":"sig","alg":"HS256","k":"` + encodedKey + `"}]}`)
	set, err := ParseJWKS(document, JWKSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := set.ResolveKey(Header{Algorithm: "HS256", KeyID: "one"})
	if err != nil || string(resolved.HMAC) != string(key) {
		t.Fatalf("ResolveKey = %#v, %v", resolved, err)
	}
	if _, err := set.ResolveKey(Header{Algorithm: "HS256", KeyID: "missing"}); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("missing key error = %v", err)
	}
	if _, err := set.ResolveKey(Header{Algorithm: "RS256", KeyID: "one"}); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("algorithm mismatch error = %v", err)
	}
	invalidSecret, err := ParseJWKS([]byte(`{"keys":[{"kty":"oct","kid":"bad","alg":"HS256","k":"not-base64!"}]}`), JWKSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invalidSecret.ResolveKey(Header{Algorithm: "HS256", KeyID: "bad"}); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("malformed secret error = %v", err)
	}
	shortSecret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{'s'}, 31))
	shortSet, err := ParseJWKS([]byte(`{"keys":[{"kty":"oct","kid":"short","alg":"HS256","k":"`+shortSecret+`"}]}`), JWKSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shortSet.ResolveKey(Header{Algorithm: "HS256", KeyID: "short"}); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("short secret error = %v", err)
	}
	ambiguous := []byte(`{"keys":[` +
		`{"kty":"oct","kid":"one","alg":"HS256","k":"` + encodedKey + `"},` +
		`{"kty":"oct","kid":"one","alg":"HS256","k":"` + encodedKey + `"}]}`)
	set, err = ParseJWKS(ambiguous, JWKSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.ResolveKey(Header{Algorithm: "HS256", KeyID: "one"}); !errors.Is(err, ErrAmbiguousKey) {
		t.Fatalf("ambiguous error = %v", err)
	}
}

func TestRFC7515AppendixA1HS256(t *testing.T) {
	const compact = "eyJ0eXAiOiJKV1QiLA0KICJhbGciOiJIUzI1NiJ9." +
		"eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ." +
		"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const encodedKey = "AyM1SysPpbyDfgZld3umj1qzKObwVMkoqQ-EstJQLr_T-1qS0gZH75aKtMN3Yj0iPS4hcgUuTwjAzZr1Z9CAow"
	key, err := base64.RawURLEncoding.DecodeString(encodedKey)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseAndVerify(compact, KeyResolverFunc(func(Header) (VerificationKey, error) {
		return VerificationKey{Algorithm: "HS256", HMAC: key}, nil
	}), ParseOptions{}, VerifyOptions{
		AllowedAlgorithms: []string{"HS256"}, TokenType: "JWT",
		Clock: func() time.Time { return time.Unix(1_300_000_000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != "joe" {
		t.Fatalf("issuer = %q", claims.Issuer)
	}
}
