package jwt

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"time"
)

const maxLeeway = 10 * time.Minute

type VerificationKey struct {
	Algorithm string
	HMAC      []byte
	RSA       *rsa.PublicKey
}

type KeyResolver interface {
	ResolveKey(header Header) (VerificationKey, error)
}

type KeyResolverFunc func(header Header) (VerificationKey, error)

func (resolve KeyResolverFunc) ResolveKey(header Header) (VerificationKey, error) {
	if resolve == nil {
		return VerificationKey{}, ErrInvalidOptions
	}
	return resolve(header)
}

type VerifyOptions struct {
	AllowedAlgorithms      []string
	Issuer                 string
	Audience               string
	TokenType              string
	AllowMissingExpiration bool
	AllowFutureIssuedAt    bool
	Clock                  func() time.Time
	Leeway                 time.Duration
}

func Verify(token *Token, resolver KeyResolver, options VerifyOptions) (Claims, error) {
	if token == nil || resolver == nil || len(options.AllowedAlgorithms) == 0 || options.Leeway < 0 || options.Leeway > maxLeeway {
		return Claims{}, ErrInvalidOptions
	}
	algorithm := token.Header.Algorithm
	if algorithm == "none" || !slices.Contains(options.AllowedAlgorithms, algorithm) {
		return Claims{}, ErrUnsupportedAlgorithm
	}
	key, err := resolver.ResolveKey(token.Header)
	if err != nil {
		if errors.Is(err, ErrAmbiguousKey) {
			return Claims{}, ErrAmbiguousKey
		}
		return Claims{}, ErrKeyNotFound
	}
	if key.Algorithm != algorithm {
		return Claims{}, ErrUnsupportedAlgorithm
	}
	if err := verifySignature(token, key); err != nil {
		return Claims{}, err
	}
	if err := validateClaims(token, options); err != nil {
		return Claims{}, err
	}
	return token.Claims, nil
}

func ParseAndVerify(compact string, resolver KeyResolver, parseOptions ParseOptions, verifyOptions VerifyOptions) (Claims, error) {
	token, err := Parse(compact, parseOptions)
	if err != nil {
		return Claims{}, err
	}
	return Verify(token, resolver, verifyOptions)
}

func verifySignature(token *Token, key VerificationKey) error {
	switch key.Algorithm {
	case "HS256":
		if len(key.HMAC) < sha256.Size {
			return ErrKeyNotFound
		}
		mac := hmac.New(sha256.New, key.HMAC)
		_, _ = mac.Write([]byte(token.signingInput))
		if !hmac.Equal(mac.Sum(nil), token.Signature) {
			return ErrInvalidSignature
		}
		return nil
	case "RS256":
		if key.RSA == nil || key.RSA.N == nil || key.RSA.N.BitLen() < 2048 || key.RSA.E < 3 {
			return ErrKeyNotFound
		}
		digest := sha256.Sum256([]byte(token.signingInput))
		if err := rsa.VerifyPKCS1v15(key.RSA, crypto.SHA256, digest[:], token.Signature); err != nil {
			return ErrInvalidSignature
		}
		return nil
	default:
		return ErrUnsupportedAlgorithm
	}
}

func validateClaims(token *Token, options VerifyOptions) error {
	now := time.Now()
	if options.Clock != nil {
		now = options.Clock()
	}
	claims := token.Claims
	if claims.ExpiresAt == nil && !options.AllowMissingExpiration {
		return fmt.Errorf("%w: expiration required", ErrInvalidClaims)
	}
	if claims.ExpiresAt != nil && !now.Before(time.Unix(*claims.ExpiresAt, 0).Add(options.Leeway)) {
		return fmt.Errorf("%w: expired", ErrInvalidClaims)
	}
	if claims.NotBefore != nil && now.Add(options.Leeway).Before(time.Unix(*claims.NotBefore, 0)) {
		return fmt.Errorf("%w: not active", ErrInvalidClaims)
	}
	if claims.IssuedAt != nil && !options.AllowFutureIssuedAt && now.Add(options.Leeway).Before(time.Unix(*claims.IssuedAt, 0)) {
		return fmt.Errorf("%w: issued in future", ErrInvalidClaims)
	}
	if options.Issuer != "" && claims.Issuer != options.Issuer {
		return fmt.Errorf("%w: issuer", ErrInvalidClaims)
	}
	if options.Audience != "" && !slices.Contains(claims.Audience, options.Audience) {
		return fmt.Errorf("%w: audience", ErrInvalidClaims)
	}
	if options.TokenType != "" && token.Header.Type != options.TokenType {
		return fmt.Errorf("%w: token type", ErrInvalidClaims)
	}
	return nil
}
