// Package jwt strictly parses, signs, and verifies a bounded signed JWT subset.
package jwt

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shibukawa/tinygodriver/jwt/internal/authn"
)

var (
	ErrMalformed            = errors.New("jwt: malformed token")
	ErrLimitExceeded        = errors.New("jwt: limit exceeded")
	ErrUnsupportedAlgorithm = errors.New("jwt: unsupported algorithm")
	ErrInvalidSignature     = errors.New("jwt: invalid signature")
	ErrInvalidClaims        = errors.New("jwt: invalid claims")
	ErrKeyNotFound          = errors.New("jwt: verification key not found")
	ErrAmbiguousKey         = errors.New("jwt: ambiguous verification key")
	ErrInvalidOptions       = errors.New("jwt: invalid options")
)

const (
	defaultMaxTokenBytes   = 16 << 10
	defaultMaxSegmentBytes = 8 << 10
	defaultMaxJSONDepth    = 16
	defaultMaxJSONMembers  = 256
	maxMaxTokenBytes       = 4 << 20
	maxMaxSegmentBytes     = 2 << 20
	maxMaxJSONDepth        = 64
	maxMaxJSONMembers      = 8192
	maxSignerKeyBytes      = 4096
)

type ParseOptions struct {
	MaxTokenBytes   int
	MaxSegmentBytes int
	MaxJSONDepth    int
	MaxJSONMembers  int
}

type Header struct {
	Algorithm string          `json:"alg"`
	Type      string          `json:"typ,omitempty"`
	KeyID     string          `json:"kid,omitempty"`
	Critical  []string        `json:"crit,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

type Token struct {
	Header    Header
	Claims    Claims
	Signature []byte

	// signingInput is the first two segments of the compact form, the bytes
	// every verification hashes. Bytes rather than a string so verifying does
	// not convert (and copy) per call.
	signingInput []byte
}

func Parse(compact string, options ParseOptions) (*Token, error) {
	options, err := normalizeParseOptions(options)
	if err != nil {
		return nil, err
	}
	if len(compact) == 0 || len(compact) > options.MaxTokenBytes {
		return nil, ErrLimitExceeded
	}
	// Two IndexByte calls in place of strings.Split: same three segments,
	// no slice allocated to hold them.
	dot1 := strings.IndexByte(compact, '.')
	if dot1 < 0 {
		return nil, ErrMalformed
	}
	dot2 := strings.IndexByte(compact[dot1+1:], '.')
	if dot2 < 0 {
		return nil, ErrMalformed
	}
	dot2 += dot1 + 1
	if strings.IndexByte(compact[dot2+1:], '.') >= 0 {
		return nil, ErrMalformed
	}
	segments := [3]string{compact[:dot1], compact[dot1+1 : dot2], compact[dot2+1:]}
	if segments[0] == "" || segments[1] == "" || segments[2] == "" {
		return nil, ErrMalformed
	}
	for _, segment := range segments {
		if len(segment) > options.MaxSegmentBytes {
			return nil, ErrLimitExceeded
		}
	}
	headerJSON, err := authn.DecodeBase64URL(segments[0], options.MaxSegmentBytes, options.MaxSegmentBytes)
	if err != nil {
		return nil, classifyDecodeError(err)
	}
	claimsJSON, err := authn.DecodeBase64URL(segments[1], options.MaxSegmentBytes, options.MaxSegmentBytes)
	if err != nil {
		return nil, classifyDecodeError(err)
	}
	signature, err := authn.DecodeBase64URL(segments[2], options.MaxSegmentBytes, options.MaxSegmentBytes)
	if err != nil {
		return nil, classifyDecodeError(err)
	}
	jsonOptions := authn.JSONOptions{
		MaxBytes: options.MaxSegmentBytes, MaxDepth: options.MaxJSONDepth, MaxMembers: options.MaxJSONMembers,
	}
	var header Header
	if err := parseHeader(headerJSON, &header, jsonOptions); err != nil {
		return nil, err
	}
	// headerJSON is a fresh buffer this function owns — DecodeBase64URL
	// allocated it, and nothing else keeps a reference — so Raw can alias it
	// instead of copying.
	header.Raw = json.RawMessage(headerJSON)
	claims, err := parseClaims(claimsJSON, jsonOptions)
	if err != nil {
		return nil, err
	}
	return &Token{
		Header: header, Claims: claims, Signature: signature,
		signingInput: []byte(compact[:dot2]),
	}, nil
}

// parseHeader validates the header object and reads the members this
// package understands, in one pass over its bytes: the validation is a scan,
// and the members it hands over are decoded in place. Unknown members are
// ignored, as json.Unmarshal ignored them; a member of the wrong type, or a
// missing or null alg, is malformed, as it was then.
//
// Member names match exactly. json.Unmarshal also matched them ignoring case,
// so a header spelling the algorithm "ALG" used to parse; it does not now,
// which is the strict reading RFC 7515 gives the names.
func parseHeader(data []byte, header *Header, options authn.JSONOptions) error {
	critical := 0
	err := authn.ObjectMembers(data, options, func(name, value []byte) error {
		switch string(name) {
		case "alg":
			return setStringMember(value, &header.Algorithm)
		case "typ":
			return setStringMember(value, &header.Type)
		case "kid":
			return setStringMember(value, &header.KeyID)
		case "crit":
			if value[0] == 'n' {
				return nil
			}
			if value[0] != '[' {
				return errMemberType
			}
			return authn.ArrayElements(value, func(elem []byte) error {
				if elem[0] != '"' {
					return errMemberType
				}
				critical++
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return classifyJSONError(err)
	}
	if header.Algorithm == "" {
		return ErrMalformed
	}
	if critical != 0 {
		return fmt.Errorf("%w: critical headers", ErrUnsupportedAlgorithm)
	}
	return nil
}

// errMemberType is a member whose value is not the type its name requires.
// It reports as ErrMalformed, which is what json.Unmarshal's type error
// became.
var errMemberType = errors.New("jwt: member of the wrong type")

// setStringMember stores a validated JSON string value into target. A null
// leaves target alone, as json.Unmarshal leaves a string field on null.
func setStringMember(value []byte, target *string) error {
	if value[0] == 'n' {
		return nil
	}
	if value[0] != '"' {
		return errMemberType
	}
	// The values every token carries are answered from constants, so a
	// header costs no string allocations for them.
	switch string(value) {
	case `"HS256"`:
		*target = "HS256"
	case `"RS256"`:
		*target = "RS256"
	case `"JWT"`:
		*target = "JWT"
	default:
		decoded, ok := authn.Unquote(value)
		if !ok {
			return errMemberType
		}
		*target = decoded
	}
	return nil
}

func normalizeParseOptions(options ParseOptions) (ParseOptions, error) {
	if options.MaxTokenBytes < 0 || options.MaxTokenBytes > maxMaxTokenBytes ||
		options.MaxSegmentBytes < 0 || options.MaxSegmentBytes > maxMaxSegmentBytes ||
		options.MaxJSONDepth < 0 || options.MaxJSONDepth > maxMaxJSONDepth ||
		options.MaxJSONMembers < 0 || options.MaxJSONMembers > maxMaxJSONMembers {
		return ParseOptions{}, ErrInvalidOptions
	}
	if options.MaxTokenBytes == 0 {
		options.MaxTokenBytes = defaultMaxTokenBytes
	}
	if options.MaxSegmentBytes == 0 {
		options.MaxSegmentBytes = defaultMaxSegmentBytes
	}
	if options.MaxJSONDepth == 0 {
		options.MaxJSONDepth = defaultMaxJSONDepth
	}
	if options.MaxJSONMembers == 0 {
		options.MaxJSONMembers = defaultMaxJSONMembers
	}
	return options, nil
}

func classifyDecodeError(err error) error {
	if errors.Is(err, authn.ErrLimitExceeded) {
		return ErrLimitExceeded
	}
	return ErrMalformed
}

func classifyJSONError(err error) error {
	if errors.Is(err, authn.ErrLimitExceeded) {
		return ErrLimitExceeded
	}
	return ErrMalformed
}
