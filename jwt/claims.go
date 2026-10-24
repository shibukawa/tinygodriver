package jwt

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/shibukawa/tinygodriver/jwt/internal/authn"
)

type Claims struct {
	Issuer    string
	Subject   string
	Audience  []string
	ExpiresAt *int64
	NotBefore *int64
	IssuedAt  *int64
	ID        string
	Raw       map[string]json.RawMessage
}

// parseClaims validates the claims object and decodes it in one pass over
// its bytes. Raw holds every member as a view of data, which this parse owns
// and never writes, so the map costs its keys and nothing per value; the
// registered claims are decoded from those same bytes.
func parseClaims(data []byte, options authn.JSONOptions) (Claims, error) {
	claims := Claims{Raw: make(map[string]json.RawMessage, 8)}
	var audience, expires, notBefore, issued []byte
	err := authn.ObjectMembers(data, options, func(name, value []byte) error {
		claims.Raw[string(name)] = json.RawMessage(value)
		switch string(name) {
		case "iss":
			return setStringMember(value, &claims.Issuer)
		case "sub":
			return setStringMember(value, &claims.Subject)
		case "jti":
			return setStringMember(value, &claims.ID)
		case "aud":
			audience = value
		case "exp":
			expires = value
		case "nbf":
			notBefore = value
		case "iat":
			issued = value
		}
		return nil
	})
	if err != nil {
		return Claims{}, classifyJSONError(err)
	}
	if claims.Audience, err = decodeAudience(audience); err != nil {
		return Claims{}, err
	}
	if claims.ExpiresAt, err = decodeNumericDate(expires); err != nil {
		return Claims{}, err
	}
	if claims.NotBefore, err = decodeNumericDate(notBefore); err != nil {
		return Claims{}, err
	}
	if claims.IssuedAt, err = decodeNumericDate(issued); err != nil {
		return Claims{}, err
	}
	return claims, nil
}

func (c Claims) Value(name string) (json.RawMessage, bool) {
	value, ok := c.Raw[name]
	return value, ok
}

func (c Claims) String(name string) (string, bool) {
	value, ok := c.Raw[name]
	if !ok {
		return "", false
	}
	var result string
	if json.Unmarshal(value, &result) != nil {
		return "", false
	}
	return result, true
}

// decodeAudience reads aud, a validated JSON value: one string, or an array
// of one to sixty-four of them, none empty.
func decodeAudience(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	switch raw[0] {
	case '"':
		one, ok := authn.Unquote(raw)
		if !ok || one == "" || len(one) > defaultMaxSegmentBytes {
			return nil, ErrMalformed
		}
		return []string{one}, nil
	case '[':
		var many []string
		err := authn.ArrayElements(raw, func(elem []byte) error {
			if elem[0] != '"' || len(many) == 64 {
				return ErrMalformed
			}
			audience, ok := authn.Unquote(elem)
			if !ok || audience == "" || len(audience) > defaultMaxSegmentBytes {
				return ErrMalformed
			}
			many = append(many, audience)
			return nil
		})
		if err != nil || len(many) == 0 {
			return nil, ErrMalformed
		}
		return many, nil
	}
	return nil, ErrMalformed
}

// decodeNumericDate reads a NumericDate claim, a validated JSON value, as the
// whole seconds it must be: an integer within int64, with no fraction and no
// exponent.
func decodeNumericDate(raw []byte) (*int64, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	value, ok := parseInt64(raw)
	if !ok {
		return nil, ErrMalformed
	}
	return &value, nil
}

// parseInt64 reads an optionally negative decimal integer that fits int64.
func parseInt64(b []byte) (int64, bool) {
	negative := false
	if len(b) > 0 && b[0] == '-' {
		negative = true
		b = b[1:]
	}
	if len(b) == 0 || len(b) > 19 {
		return 0, false
	}
	var n uint64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	if negative {
		if n > 1<<63 {
			return 0, false
		}
		return -int64(n), true
	}
	if n > math.MaxInt64 {
		return 0, false
	}
	return int64(n), true
}

func (c Claims) MarshalJSON() ([]byte, error) {
	result := make(map[string]json.RawMessage, len(c.Raw)+7)
	for name, value := range c.Raw {
		result[name] = append(json.RawMessage(nil), value...)
	}
	setStringClaim(result, "iss", c.Issuer)
	setStringClaim(result, "sub", c.Subject)
	setStringClaim(result, "jti", c.ID)
	if len(c.Audience) == 1 {
		result["aud"], _ = json.Marshal(c.Audience[0])
	} else if len(c.Audience) > 1 {
		result["aud"], _ = json.Marshal(c.Audience)
	}
	setNumericClaim(result, "exp", c.ExpiresAt)
	setNumericClaim(result, "nbf", c.NotBefore)
	setNumericClaim(result, "iat", c.IssuedAt)
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, errors.Join(ErrMalformed, err)
	}
	return encoded, nil
}

func setStringClaim(result map[string]json.RawMessage, name, value string) {
	if value != "" {
		result[name], _ = json.Marshal(value)
	}
}

func setNumericClaim(result map[string]json.RawMessage, name string, value *int64) {
	if value != nil {
		result[name] = json.RawMessage(strconv.FormatInt(*value, 10))
	}
}
