//go:build !tinygo

package jwt

import "testing"

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"eyJhbGciOiJIUzI1NiJ9.eyJleHAiOjQxMDI0NDQ4MDB9.AA",
		"eyJhbGciOiJub25lIn0.e30.AA",
		"..",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, compact string) {
		_, _ = Parse(compact, ParseOptions{MaxTokenBytes: 4096, MaxSegmentBytes: 2048})
	})
}

func FuzzParseJWKS(f *testing.F) {
	f.Add([]byte(`{"keys":[]}`))
	f.Add([]byte(`{"keys":[{"kty":"RSA","kid":"k","alg":"RS256","n":"AA","e":"AQAB"}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseJWKS(data, JWKSOptions{MaxBytes: 4096, MaxKeys: 16})
	})
}
