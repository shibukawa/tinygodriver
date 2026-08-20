package jwt

import (
	"encoding/base64"
	"errors"
	"testing"
)

// Trailing content after the claims JSON is rejected by authn.ValidateJSON
// before parseClaims ever runs. parseClaims may therefore assume its input is
// exactly one JSON value; this pins the assumption so a change to the
// validation order cannot silently start feeding it trailing garbage.
func TestParseRejectsTrailingClaimsContent(t *testing.T) {
	encode := base64.RawURLEncoding.EncodeToString
	header := encode([]byte(`{"alg":"HS256"}`))
	signature := encode([]byte("signature-bytes-signature-bytes!"))

	for _, claims := range []string{
		`{"iss":"a"}x`,
		`{"iss":"a"}{"iss":"b"}`,
		`{"iss":"a"} 1`,
		`{"iss":"a"}[]`,
	} {
		compact := header + "." + encode([]byte(claims)) + "." + signature
		if _, err := Parse(compact, ParseOptions{}); !errors.Is(err, ErrMalformed) {
			t.Errorf("Parse with claims %q: got %v, want ErrMalformed", claims, err)
		}
	}

	// Trailing whitespace, by contrast, is legal JSON framing and stays
	// accepted; the pin covers both directions so the boundary cannot move.
	compact := header + "." + encode([]byte(`{"iss":"a"}`+"\n ")) + "." + signature
	if _, err := Parse(compact, ParseOptions{}); err != nil {
		t.Errorf("Parse with trailing whitespace after claims: got %v, want nil", err)
	}
}
