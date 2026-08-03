package jwt

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
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

func parseClaims(data []byte) (Claims, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil || raw == nil {
		return Claims{}, ErrMalformed
	}
	claims := Claims{Raw: raw}
	if err := decodeOptionalString(raw, "iss", &claims.Issuer); err != nil {
		return Claims{}, err
	}
	if err := decodeOptionalString(raw, "sub", &claims.Subject); err != nil {
		return Claims{}, err
	}
	if err := decodeOptionalString(raw, "jti", &claims.ID); err != nil {
		return Claims{}, err
	}
	var err error
	if claims.Audience, err = decodeAudience(raw["aud"]); err != nil {
		return Claims{}, err
	}
	if claims.ExpiresAt, err = decodeNumericDate(raw["exp"]); err != nil {
		return Claims{}, err
	}
	if claims.NotBefore, err = decodeNumericDate(raw["nbf"]); err != nil {
		return Claims{}, err
	}
	if claims.IssuedAt, err = decodeNumericDate(raw["iat"]); err != nil {
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

func decodeOptionalString(raw map[string]json.RawMessage, name string, target *string) error {
	value, ok := raw[name]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(value, target); err != nil {
		return ErrMalformed
	}
	return nil
}

func decodeAudience(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one == "" || len(one) > defaultMaxSegmentBytes {
			return nil, ErrMalformed
		}
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil || many == nil {
		return nil, ErrMalformed
	}
	if len(many) == 0 || len(many) > 64 {
		return nil, ErrMalformed
	}
	for _, audience := range many {
		if audience == "" || len(audience) > defaultMaxSegmentBytes {
			return nil, ErrMalformed
		}
	}
	return many, nil
}

func decodeNumericDate(raw json.RawMessage) (*int64, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if bytes.ContainsAny(raw, ".eE") {
		return nil, ErrMalformed
	}
	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return nil, ErrMalformed
	}
	return &value, nil
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
