package jwt

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"math"
	"math/big"

	"github.com/shibukawa/tinygodriver/jwt/internal/authn"
)

const (
	defaultMaxJWKSBytes = 64 << 10
	defaultMaxJWKSKeys  = 64
	maximumMaxJWKSBytes = 16 << 20
	maximumMaxJWKSKeys  = 4096
)

type JWKSOptions struct {
	MaxBytes int
	MaxKeys  int
}

type JWKS struct {
	keys []jwk
}

type jwk struct {
	KeyID     string `json:"kid"`
	KeyType   string `json:"kty"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
	Secret    string `json:"k"`
}

func ParseJWKS(data []byte, options JWKSOptions) (*JWKS, error) {
	if options.MaxBytes < 0 || options.MaxKeys < 0 || options.MaxBytes > maximumMaxJWKSBytes || options.MaxKeys > maximumMaxJWKSKeys {
		return nil, ErrInvalidOptions
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = defaultMaxJWKSBytes
	}
	if options.MaxKeys == 0 {
		options.MaxKeys = defaultMaxJWKSKeys
	}
	if len(data) > options.MaxBytes {
		return nil, ErrLimitExceeded
	}
	if err := authn.ValidateJSON(data, authn.JSONOptions{
		MaxBytes: options.MaxBytes, MaxDepth: 8, MaxMembers: options.MaxKeys*16 + 8,
	}); err != nil {
		if errors.Is(err, authn.ErrLimitExceeded) {
			return nil, ErrLimitExceeded
		}
		return nil, ErrMalformed
	}
	var document struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(data, &document); err != nil || document.Keys == nil {
		return nil, ErrMalformed
	}
	if len(document.Keys) > options.MaxKeys {
		return nil, ErrLimitExceeded
	}
	keys := make([]jwk, 0, len(document.Keys))
	for _, key := range document.Keys {
		if key.KeyID == "" || key.Algorithm == "" || (key.Use != "" && key.Use != "sig") {
			continue
		}
		switch {
		case key.KeyType == "oct" && key.Algorithm == "HS256" && key.Secret != "":
		case key.KeyType == "RSA" && key.Algorithm == "RS256" && key.Modulus != "" && key.Exponent != "":
		default:
			continue
		}
		keys = append(keys, key)
	}
	return &JWKS{keys: keys}, nil
}

func (set *JWKS) ResolveKey(header Header) (VerificationKey, error) {
	if set == nil || header.KeyID == "" || header.Algorithm == "" {
		return VerificationKey{}, ErrKeyNotFound
	}
	var match *jwk
	for index := range set.keys {
		candidate := &set.keys[index]
		if candidate.KeyID != header.KeyID || candidate.Algorithm != header.Algorithm {
			continue
		}
		if match != nil {
			return VerificationKey{}, ErrAmbiguousKey
		}
		match = candidate
	}
	if match == nil {
		return VerificationKey{}, ErrKeyNotFound
	}
	switch match.KeyType {
	case "oct":
		secret, err := authn.DecodeBase64URL(match.Secret, 2048, 1024)
		if err != nil || len(secret) < 32 {
			return VerificationKey{}, ErrKeyNotFound
		}
		return VerificationKey{Algorithm: "HS256", HMAC: secret}, nil
	case "RSA":
		modulus, err := authn.DecodeBase64URL(match.Modulus, 4096, 2048)
		if err != nil || len(modulus) < 256 {
			return VerificationKey{}, ErrKeyNotFound
		}
		exponentBytes, err := authn.DecodeBase64URL(match.Exponent, 8, 4)
		if err != nil || len(exponentBytes) == 0 {
			return VerificationKey{}, ErrKeyNotFound
		}
		exponent := 0
		for _, value := range exponentBytes {
			if exponent > (math.MaxInt-int(value))/256 {
				return VerificationKey{}, ErrKeyNotFound
			}
			exponent = exponent*256 + int(value)
		}
		if exponent < 3 || exponent%2 == 0 {
			return VerificationKey{}, ErrKeyNotFound
		}
		return VerificationKey{
			Algorithm: "RS256",
			RSA:       &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent},
		}, nil
	default:
		return VerificationKey{}, ErrKeyNotFound
	}
}
