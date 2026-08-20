package authn

import (
	"bytes"
	"encoding/base64"
	"math/rand"
	"strings"
	"testing"
)

// referenceDecodeBase64URL is the decoder as originally written: decode, then
// re-encode and compare, which by construction accepts exactly the canonical
// encodings. It stays here as the oracle DecodeBase64URL is checked against,
// so the faster canonicality check cannot drift from it unnoticed.
func referenceDecodeBase64URL(value string, maxEncodedBytes, maxDecodedBytes int) ([]byte, error) {
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

func compareAgainstReference(t *testing.T, input string) {
	t.Helper()
	got, gotErr := DecodeBase64URL(input, 4096, 4096)
	want, wantErr := referenceDecodeBase64URL(input, 4096, 4096)
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("DecodeBase64URL(%q) error %v, reference error %v", input, gotErr, wantErr)
	}
	if gotErr != nil {
		return
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("DecodeBase64URL(%q) = %x, reference %x", input, got, want)
	}
}

// TestDecodeBase64URLMatchesReference sweeps every input up to four characters
// over an alphabet of valid and invalid bytes, with the final position — the
// one the trailing-bits rule judges — always exhausting the full base64url
// alphabet. Longer inputs are randomized: valid encodings, then mutations.
func TestDecodeBase64URLMatchesReference(t *testing.T) {
	const b64alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	// \r and \n matter here: encoding/base64's decoder silently skips them,
	// so a canonicality check that leans on DecodeString alone would accept
	// "A\nA" while the reference rejects it.
	full := b64alphabet + "=+/~ \r\n\x00\xffあ"
	// Leading positions vary less: the canonicality rule only judges the final
	// quantum, so a few bit patterns plus invalid bytes cover them.
	lead := "AQaz09-_=+"

	compareAgainstReference(t, "")
	for _, c := range []byte(full) {
		compareAgainstReference(t, string(c))
	}
	for _, c0 := range []byte(full) {
		for _, c1 := range []byte(full) {
			compareAgainstReference(t, string([]byte{c0, c1}))
		}
	}
	for _, c0 := range []byte(lead) {
		for _, c1 := range []byte(lead) {
			for _, c2 := range []byte(full) {
				compareAgainstReference(t, string([]byte{c0, c1, c2}))
			}
			for _, c2 := range []byte(lead) {
				for _, c3 := range []byte(full) {
					compareAgainstReference(t, string([]byte{c0, c1, c2, c3}))
				}
			}
		}
	}

	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		raw := make([]byte, 1+rng.Intn(64))
		rng.Read(raw)
		valid := base64.RawURLEncoding.EncodeToString(raw)
		compareAgainstReference(t, valid)

		mutated := []byte(valid)
		mutated[rng.Intn(len(mutated))] = full[rng.Intn(len(full))]
		compareAgainstReference(t, string(mutated))

		// Truncations change which trailing-quantum rule applies.
		compareAgainstReference(t, valid[:rng.Intn(len(valid)+1)])
	}
}
