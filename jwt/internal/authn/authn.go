// Package authn contains the bounded parsing primitives the jwt package
// shares. Protocol-specific algorithm and key policy does not belong in this
// package.
package authn

import (
	"encoding/base64"
	"errors"
	"strings"
)

var (
	ErrInvalidSize     = errors.New("authn: invalid size")
	ErrInvalidEncoding = errors.New("authn: invalid encoding")
	ErrLimitExceeded   = errors.New("authn: limit exceeded")
)

// DecodeBase64URL strictly decodes canonical, unpadded Base64url.
//
// Canonicality used to be checked by re-encoding the result and comparing,
// which copied every segment twice more. DecodeString already rejects padding,
// wrong lengths, and bytes outside the alphabet, with two exceptions this
// function closes itself: it skips \r and \n instead of failing, and it
// accepts a final quantum whose unused low bits are not zero. The exhaustive
// test in this package holds the sum of these checks equal to the re-encoding
// oracle.
func DecodeBase64URL(value string, maxEncodedBytes, maxDecodedBytes int) ([]byte, error) {
	if maxEncodedBytes <= 0 || maxDecodedBytes <= 0 {
		return nil, ErrInvalidSize
	}
	if len(value) > maxEncodedBytes || base64.RawURLEncoding.DecodedLen(len(value)) > maxDecodedBytes {
		return nil, ErrLimitExceeded
	}
	if strings.ContainsAny(value, "=\r\n") {
		return nil, ErrInvalidEncoding
	}
	switch len(value) % 4 {
	case 2:
		// The final two characters carry 12 bits for one byte; the low 4 must
		// be zero or the encoding is one of several spellings of that byte.
		if sextet(value[len(value)-1])&0x0F != 0 {
			return nil, ErrInvalidEncoding
		}
	case 3:
		// Three characters carry 18 bits for two bytes; the low 2 must be zero.
		if sextet(value[len(value)-1])&0x03 != 0 {
			return nil, ErrInvalidEncoding
		}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, ErrInvalidEncoding
	}
	return decoded, nil
}

// sextet returns the 6-bit value of a base64url character, or 0xFF for a byte
// outside the alphabet. 0xFF fails either trailing-bits test, which is the
// same ErrInvalidEncoding that DecodeString would have produced for the byte.
func sextet(c byte) byte {
	switch {
	case 'A' <= c && c <= 'Z':
		return c - 'A'
	case 'a' <= c && c <= 'z':
		return c - 'a' + 26
	case '0' <= c && c <= '9':
		return c - '0' + 52
	case c == '-':
		return 62
	case c == '_':
		return 63
	}
	return 0xFF
}
