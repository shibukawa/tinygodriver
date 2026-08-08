package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
)

type Signer interface {
	Algorithm() string
	Sign(signingInput []byte) ([]byte, error)
}

type HMACSigner struct {
	key []byte
}

func NewHMACSigner(key []byte) (*HMACSigner, error) {
	if len(key) < sha256.Size || len(key) > maxSignerKeyBytes {
		return nil, ErrInvalidOptions
	}
	return &HMACSigner{key: append([]byte(nil), key...)}, nil
}

func (*HMACSigner) Algorithm() string { return "HS256" }

func (s *HMACSigner) Sign(signingInput []byte) ([]byte, error) {
	if s == nil || len(s.key) < sha256.Size {
		return nil, ErrInvalidOptions
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(signingInput)
	return mac.Sum(nil), nil
}

// signingAlgorithms is what Sign will emit. It is deliberately the same set
// verifySignature accepts: a Signer reporting anything else would produce a
// token this package could not check, which is a shape worth refusing even
// though the signer, not this package, would be the one at fault. The list
// therefore lives with the RS256 build tag split, in verify_rs256.go and
// verify_rs256_disabled.go: a build without RSA verification must not emit
// RS256 either.
//
// RS256 signing is this repository's divergence from upstream, which shipped
// HS256 only. See README.md.

func canSign(algorithm string) bool {
	for _, supported := range signingAlgorithms {
		if algorithm == supported {
			return true
		}
	}
	return false
}

func Sign(header Header, claims Claims, signer Signer) (string, error) {
	if signer == nil || !canSign(signer.Algorithm()) {
		return "", ErrUnsupportedAlgorithm
	}
	if header.Algorithm == "" {
		header.Algorithm = signer.Algorithm()
	}
	if header.Algorithm != signer.Algorithm() || len(header.Critical) != 0 {
		return "", ErrUnsupportedAlgorithm
	}
	header.Raw = nil
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", errors.Join(ErrMalformed, err)
	}
	if len(headerJSON) > maxMaxSegmentBytes {
		return "", ErrLimitExceeded
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	if len(claimsJSON) > maxMaxSegmentBytes {
		return "", ErrLimitExceeded
	}
	encode := base64.RawURLEncoding.EncodeToString
	signingInput := encode(headerJSON) + "." + encode(claimsJSON)
	signature, err := signer.Sign([]byte(signingInput))
	if err != nil {
		return "", err
	}
	if len(signature) == 0 {
		return "", ErrInvalidSignature
	}
	compact := signingInput + "." + encode(signature)
	if len(compact) > maxMaxTokenBytes {
		return "", ErrLimitExceeded
	}
	return compact, nil
}
