package xml

import (
	"bytes"
	"strconv"
	"unicode/utf8"
	"unsafe"
)

// Value is attribute or text content as it appears in the document, entities
// included. Its methods decode on demand, so content with no ampersand,
// which is nearly all of it, is never copied. A Value aliases the reader's
// buffer and is valid until the reader advances.
type Value []byte

// HasEntities reports whether decoding would change the bytes.
func (v Value) HasEntities() bool { return bytes.IndexByte(v, '&') >= 0 }

// AppendTo appends the decoded content to dst.
func (v Value) AppendTo(dst []byte) []byte {
	if !v.HasEntities() {
		return append(dst, v...)
	}
	return Unescape(dst, v)
}

// String returns the decoded content as a new string.
func (v Value) String() string {
	if !v.HasEntities() {
		return string(v)
	}
	return string(Unescape(nil, v))
}

// Equal reports whether the decoded content equals s.
func (v Value) Equal(s string) bool {
	if !v.HasEntities() {
		return string(v) == s
	}
	var tmp [64]byte
	return string(Unescape(tmp[:0], v)) == s
}

// EqualFold reports whether the decoded content equals s under ASCII case
// folding.
func (v Value) EqualFold(s string) bool {
	if len(v) != len(s) {
		return false
	}
	for i := range v {
		a, b := v[i], s[i]
		if 'A' <= a && a <= 'Z' {
			a += 'a' - 'A'
		}
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}

// str views v as a string without copying. The view must not outlive v, and
// nothing here lets it: strconv returns the numbers by value and copies the
// text into any error it builds only through the caller's own conversion.
func (v Value) str() string { return unsafe.String(unsafe.SliceData(v), len(v)) }

// Int parses a decimal integer. Numbers carry no entities, so the raw bytes
// are parsed directly.
func (v Value) Int() (int64, error) {
	n, err := strconv.ParseInt(v.str(), 10, 64)
	if err != nil {
		// The error holds the input, which must not alias the buffer.
		_, err = strconv.ParseInt(string(v), 10, 64)
	}
	return n, err
}

// Uint parses a decimal unsigned integer.
func (v Value) Uint() (uint64, error) {
	n, err := strconv.ParseUint(v.str(), 10, 64)
	if err != nil {
		_, err = strconv.ParseUint(string(v), 10, 64)
	}
	return n, err
}

// Float parses a floating point number.
func (v Value) Float() (float64, error) {
	f, err := strconv.ParseFloat(v.str(), 64)
	if err != nil {
		_, err = strconv.ParseFloat(string(v), 64)
	}
	return f, err
}

// Bool parses an xsd:boolean: "1", "0", "true" or "false". Office attributes
// use the digits.
func (v Value) Bool() (bool, error) {
	switch string(v) {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	}
	return false, &strconv.NumError{Func: "Bool", Num: string(v), Err: strconv.ErrSyntax}
}

// Unescape appends src to dst with the five predefined entities and numeric
// character references decoded. A reference it does not recognize is copied
// as written.
func Unescape(dst, src []byte) []byte {
	for {
		i := bytes.IndexByte(src, '&')
		if i < 0 {
			return append(dst, src...)
		}
		dst = append(dst, src[:i]...)
		src = src[i:]
		j := bytes.IndexByte(src, ';')
		if j < 0 || j > 10 {
			dst = append(dst, '&')
			src = src[1:]
			continue
		}
		ref := src[1:j]
		var ok bool
		dst, ok = appendReference(dst, ref)
		if !ok {
			dst = append(dst, src[:j+1]...)
		}
		src = src[j+1:]
	}
}

func appendReference(dst, ref []byte) ([]byte, bool) {
	switch string(ref) {
	case "lt":
		return append(dst, '<'), true
	case "gt":
		return append(dst, '>'), true
	case "amp":
		return append(dst, '&'), true
	case "apos":
		return append(dst, '\''), true
	case "quot":
		return append(dst, '"'), true
	}
	if len(ref) < 2 || ref[0] != '#' {
		return dst, false
	}
	var n uint64
	var err error
	if ref[1] == 'x' || ref[1] == 'X' {
		n, err = strconv.ParseUint(unsafe.String(unsafe.SliceData(ref[2:]), len(ref)-2), 16, 32)
	} else {
		n, err = strconv.ParseUint(unsafe.String(unsafe.SliceData(ref[1:]), len(ref)-1), 10, 32)
	}
	if err != nil || !utf8.ValidRune(rune(n)) {
		return dst, false
	}
	return utf8.AppendRune(dst, rune(n)), true
}
