// Package base64 implements RFC 4648 Base64 with the encoding/base64 API.
// Standard and URL alphabets use SIMD on supported builds; custom alphabets,
// final quanta and malformed input retain the standard library's semantics.
package base64

import (
	std "encoding/base64"
	"io"
	"slices"
	"unsafe"
)

const (
	StdPadding       = std.StdPadding
	NoPadding        = std.NoPadding
	standardAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	urlAlphabet      = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)

// CorruptInputError identifies an invalid input byte at its absolute offset.
type CorruptInputError = std.CorruptInputError

// Encoding is an encoding/base64-compatible encoding. Its methods are safe
// for concurrent use. The zero value has the standard library's zero-value
// behavior; use NewEncoding or one of the four predefined encodings normally.
type Encoding struct {
	enc  std.Encoding
	kind uint8 // 0: custom, 1: standard, 2: URL
	pad  rune
}

var (
	StdEncoding    = NewEncoding(standardAlphabet)
	URLEncoding    = NewEncoding(urlAlphabet)
	RawStdEncoding = StdEncoding.WithPadding(NoPadding)
	RawURLEncoding = URLEncoding.WithPadding(NoPadding)
)

// NewEncoding creates a padded encoding using a 64-byte alphabet.
func NewEncoding(alphabet string) *Encoding {
	e := &Encoding{enc: *std.NewEncoding(alphabet), pad: StdPadding}
	switch alphabet {
	case standardAlphabet:
		e.kind = 1
	case urlAlphabet:
		e.kind = 2
	}
	return e
}

// WithPadding returns a copy with the specified padding, or NoPadding.
func (e Encoding) WithPadding(padding rune) *Encoding {
	e.enc = *e.enc.WithPadding(padding)
	e.pad = padding
	return &e
}

// Strict requires unused trailing bits to be zero. CR and LF remain ignored.
func (e Encoding) Strict() *Encoding {
	e.enc = *e.enc.Strict()
	return &e
}

// EncodedLen returns the encoded length of n input bytes.
func (e *Encoding) EncodedLen(n int) int { return e.enc.EncodedLen(n) }

// DecodedLen returns the maximum decoded length of n encoded bytes.
func (e *Encoding) DecodedLen(n int) int { return e.enc.DecodedLen(n) }

// Encode encodes src into dst, which must have at least EncodedLen(len(src)) bytes.
func (e *Encoding) Encode(dst, src []byte) {
	if vectorAvailable && e.kind != 0 && len(src) >= 64 {
		n := encodeVector(dst, src, e.kind == 2)
		e.enc.Encode(dst[n/3*4:], src[n:])
		return
	}
	e.enc.Encode(dst, src)
}

// AppendEncode appends the encoding of src to dst.
func (e *Encoding) AppendEncode(dst, src []byte) []byte {
	n := e.EncodedLen(len(src))
	dst = slices.Grow(dst, n)
	e.Encode(dst[len(dst):][:n], src)
	return dst[:len(dst)+n]
}

// EncodeToString returns the encoding of src as a string.
func (e *Encoding) EncodeToString(src []byte) string {
	buf := make([]byte, e.EncodedLen(len(src)))
	e.Encode(buf, src)
	return string(buf)
}

// Decode decodes src into dst and ignores CR/LF. Invalid input returns the
// successfully decoded prefix and the same CorruptInputError as encoding/base64.
func (e *Encoding) Decode(dst, src []byte) (n int, err error) {
	if !vectorAvailable || e.kind == 0 || len(src) < 64 {
		return e.enc.Decode(dst, src)
	}
	si := 0
	for len(src)-si >= 16 && len(dst)-n >= 16 {
		consumed, produced := decodeVector(dst[n:], src[si:], e.kind == 2)
		si += consumed
		n += produced
		if len(src)-si < 16 || len(dst)-n < 16 {
			break
		}
		// Find the first newline in the rejected vector. Decode the line's
		// entire scalar fringe in one call, rather than retrying SIMD after
		// each four characters before that newline. Padding or a rejected
		// vector without newlines must see the entire remaining suffix.
		end := si
		for end < si+16 && src[end] != '\r' && src[end] != '\n' {
			if rune(src[end]) == e.pad {
				return e.decodeSuffix(dst, src, si, n)
			}
			end++
		}
		if end == si+16 {
			return e.decodeSuffix(dst, src, si, n)
		}
		digits := end - si
		for end < len(src) {
			c := src[end]
			if c != '\r' && c != '\n' {
				if digits%4 == 0 {
					break
				}
				if rune(c) == e.pad {
					return e.decodeSuffix(dst, src, si, n)
				}
				digits++
			}
			end++
		}
		written, err := e.enc.Decode(dst[n:], src[si:end])
		n += written
		if err != nil {
			return n, offsetError(err, si)
		}
		si = end
	}
	return e.decodeSuffix(dst, src, si, n)
}

func (e *Encoding) decodeSuffix(dst, src []byte, si, n int) (int, error) {
	nn, err := e.enc.Decode(dst[n:], src[si:])
	return n + nn, offsetError(err, si)
}

func offsetError(err error, offset int) error {
	if corrupt, ok := err.(std.CorruptInputError); ok {
		return std.CorruptInputError(int64(corrupt) + int64(offset))
	}
	return err
}

// AppendDecode appends decoded data to dst, returning the decoded prefix on error.
func (e *Encoding) AppendDecode(dst, src []byte) ([]byte, error) {
	n := len(src)
	for n > 0 && rune(src[n-1]) == e.pad {
		n--
	}
	// Match encoding/base64's unpadded allocation bound.
	n = n/4*3 + n%4*6/8
	dst = slices.Grow(dst, n)
	written, err := e.Decode(dst[len(dst):][:n], src)
	return dst[:len(dst)+written], err
}

// DecodeString returns decoded bytes, or the successfully decoded prefix and error.
func (e *Encoding) DecodeString(s string) ([]byte, error) {
	dst := make([]byte, e.DecodedLen(len(s)))
	// All decoders only read src, including the TinyGo C backend. Borrowing
	// the string avoids a second allocation on compilers without this
	// string-to-byte-slice optimization.
	src := unsafe.Slice(unsafe.StringData(s), len(s))
	n, err := e.Decode(dst, src)
	return dst[:n], err
}

// NewEncoder returns the standard library's streaming encoder. SIMD currently
// accelerates the whole-buffer methods rather than the streaming adapters.
func NewEncoder(e *Encoding, w io.Writer) io.WriteCloser { return std.NewEncoder(&e.enc, w) }

// NewDecoder returns the standard library's streaming decoder.
func NewDecoder(e *Encoding, r io.Reader) io.Reader { return std.NewDecoder(&e.enc, r) }
