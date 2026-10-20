// Package hex implements hexadecimal encoding and decoding with the standard
// encoding/hex API and optional bounded SIMD kernels for Go and TinyGo.
package hex

import (
	std "encoding/hex"
	"io"
	"slices"
	"unsafe"
)

var ErrLength = std.ErrLength

type InvalidByteError = std.InvalidByteError

func EncodedLen(n int) int { return std.EncodedLen(n) }
func DecodedLen(n int) int { return std.DecodedLen(n) }

// Encode writes lowercase hex and returns EncodedLen(len(src)).
func Encode(dst, src []byte) int {
	if vectorAvailable && len(src) >= 32 && len(dst) >= EncodedLen(len(src)) {
		n := encodeVector(dst, src)
		return 2*n + std.Encode(dst[2*n:], src[n:])
	}
	return std.Encode(dst, src)
}

// Decode accepts either hex letter case and returns the decoded prefix on error.
func Decode(dst, src []byte) (int, error) {
	if vectorAvailable && len(src) >= 64 && len(dst) >= DecodedLen(len(src)) {
		n := decodeVector(dst, src)
		written, err := std.Decode(dst[n/2:], src[n:])
		return n/2 + written, err
	}
	return std.Decode(dst, src)
}

func AppendEncode(dst, src []byte) []byte {
	n := EncodedLen(len(src))
	dst = slices.Grow(dst, n)
	Encode(dst[len(dst):][:n], src)
	return dst[:len(dst)+n]
}

func AppendDecode(dst, src []byte) ([]byte, error) {
	n := DecodedLen(len(src))
	dst = slices.Grow(dst, n)
	n, err := Decode(dst[len(dst):][:n], src)
	return dst[:len(dst)+n], err
}

func EncodeToString(src []byte) string {
	dst := make([]byte, EncodedLen(len(src)))
	Encode(dst, src)
	return string(dst)
}

func DecodeString(s string) ([]byte, error) {
	dst := make([]byte, DecodedLen(len(s)))
	// Kernels only read src, so borrowing the immutable string is safe.
	src := unsafe.Slice(unsafe.StringData(s), len(s))
	n, err := Decode(dst, src)
	return dst[:n], err
}

// Streaming and dump formatting retain the standard library's adapters.
func NewEncoder(w io.Writer) io.Writer  { return std.NewEncoder(w) }
func NewDecoder(r io.Reader) io.Reader  { return std.NewDecoder(r) }
func Dumper(w io.Writer) io.WriteCloser { return std.Dumper(w) }
func Dump(data []byte) string           { return std.Dump(data) }
