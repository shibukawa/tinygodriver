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
	keys []resolvedKey
}

// resolvedKey is one usable entry of the set with its key material already
// decoded, so ResolveKey hands out a prebuilt value instead of re-running the
// base64 and big.Int work on every verification.
//
// An entry whose material does not decode is kept with ok false rather than
// dropped: ResolveKey has always matched on kid and alg alone, reported the
// bad material as ErrKeyNotFound, and counted such an entry toward ambiguity.
// Dropping it at parse time would change all three answers.
type resolvedKey struct {
	keyID     string
	algorithm string
	key       VerificationKey
	ok        bool
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
	keys := make([]resolvedKey, 0, len(document.Keys))
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
		verification, ok := resolveJWK(key)
		keys = append(keys, resolvedKey{
			keyID: key.KeyID, algorithm: key.Algorithm, key: verification, ok: ok,
		})
	}
	return &JWKS{keys: keys}, nil
}

// resolveJWK decodes one entry's key material into a VerificationKey. It runs
// once at parse time; the checks are the ones ResolveKey used to run per
// verification, so what it refuses and what it accepts is unchanged.
func resolveJWK(key jwk) (VerificationKey, bool) {
	switch key.KeyType {
	case "oct":
		secret, err := authn.DecodeBase64URL(key.Secret, 2048, 1024)
		if err != nil || len(secret) < 32 {
			return VerificationKey{}, false
		}
		return VerificationKey{Algorithm: "HS256", HMAC: secret}, true
	case "RSA":
		modulus, err := authn.DecodeBase64URL(key.Modulus, 4096, 2048)
		if err != nil || len(modulus) < 256 {
			return VerificationKey{}, false
		}
		exponentBytes, err := authn.DecodeBase64URL(key.Exponent, 8, 4)
		if err != nil || len(exponentBytes) == 0 {
			return VerificationKey{}, false
		}
		exponent := 0
		for _, value := range exponentBytes {
			if exponent > (math.MaxInt-int(value))/256 {
				return VerificationKey{}, false
			}
			exponent = exponent*256 + int(value)
		}
		if exponent < 3 || exponent%2 == 0 {
			return VerificationKey{}, false
		}
		return VerificationKey{
			Algorithm: "RS256",
			RSA:       &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent},
		}, true
	default:
		return VerificationKey{}, false
	}
}

func (set *JWKS) ResolveKey(header Header) (VerificationKey, error) {
	if set == nil || header.KeyID == "" || header.Algorithm == "" {
		return VerificationKey{}, ErrKeyNotFound
	}
	var match *resolvedKey
	for index := range set.keys {
		candidate := &set.keys[index]
		if candidate.keyID != header.KeyID || candidate.algorithm != header.Algorithm {
			continue
		}
		if match != nil {
			return VerificationKey{}, ErrAmbiguousKey
		}
		match = candidate
	}
	if match == nil || !match.ok {
		return VerificationKey{}, ErrKeyNotFound
	}
	return match.key, nil
}
