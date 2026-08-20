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
	if err := authn.ValidateJSON(headerJSON, jsonOptions); err != nil {
		return nil, classifyJSONError(err)
	}
	if err := authn.ValidateJSON(claimsJSON, jsonOptions); err != nil {
		return nil, classifyJSONError(err)
	}
	var header Header
	if err := json.Unmarshal(headerJSON, &header); err != nil || header.Algorithm == "" {
		return nil, ErrMalformed
	}
	if len(header.Critical) != 0 {
		return nil, fmt.Errorf("%w: critical headers", ErrUnsupportedAlgorithm)
	}
	// headerJSON is a fresh buffer this function owns — DecodeBase64URL
	// allocated it, and json.Unmarshal keeps no reference — so Raw can alias
	// it instead of copying.
	header.Raw = json.RawMessage(headerJSON)
	claims, err := parseClaims(claimsJSON)
	if err != nil {
		return nil, err
	}
	return &Token{
		Header: header, Claims: claims, Signature: signature,
		signingInput: []byte(compact[:dot2]),
	}, nil
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
