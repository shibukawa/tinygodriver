package xml

import (
	"bytes"
	"strconv"
	"unicode/utf8"
	"unsafe"
)

// Equal reports whether b holds exactly the bytes of s. It allocates on
// neither compiler: the Go compiler elides the conversion in string(b) == s,
// but TinyGo does not, and copies b for every comparison, every switch on
// string(b) and every map index by string(b). Compare names through this,
// NameIs or Value.Equal when the binary is a TinyGo one.
func Equal(b []byte, s string) bool {
	return len(b) == len(s) && unsafe.String(unsafe.SliceData(b), len(b)) == s
}

// Value is attribute or text content as it appears in the document, entities
// included. Its methods decode on demand, so content with no ampersand,
// which is nearly all of it, is never copied. A Value aliases the reader's
// buffer and is valid until the reader advances.
//
// Decoding replaces the five predefined entities and numeric character
// references, and normalizes line ends as XML requires of text: "\r\n" and a
// lone "\r" both become "\n". The further whitespace normalization XML
// applies to attribute values, tabs and newlines to spaces, is not done;
// Office writers escape such characters as references, which decoding
// leaves as the characters they name.
type Value []byte

// HasEntities reports whether decoding would change the bytes: an entity or
// character reference, or a carriage return.
func (v Value) HasEntities() bool {
	return bytes.IndexByte(v, '&') >= 0 || bytes.IndexByte(v, '\r') >= 0
}

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
		return Equal(v, s)
	}
	var tmp [64]byte
	return Equal(Unescape(tmp[:0], v), s)
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

// Float parses a floating point number. A plain decimal of at most 15
// significant digits and at most 22 fraction digits, which is every number a
// spreadsheet writer emits for a cell, is converted by one exact division
// and is bit-identical to strconv's answer; anything else goes to strconv.
func (v Value) Float() (float64, error) {
	if f, ok := parseDecimalFast(v); ok {
		return f, nil
	}
	f, err := strconv.ParseFloat(v.str(), 64)
	if err != nil {
		_, err = strconv.ParseFloat(string(v), 64)
	}
	return f, err
}

// Bool parses an xsd:boolean: "1", "0", "true" or "false". Office attributes
// use the digits.
func (v Value) Bool() (bool, error) {
	switch {
	case Equal(v, "1"), Equal(v, "true"):
		return true, nil
	case Equal(v, "0"), Equal(v, "false"):
		return false, nil
	}
	return false, &strconv.NumError{Func: "Bool", Num: string(v), Err: strconv.ErrSyntax}
}

// Unescape appends src to dst with the five predefined entities and numeric
// character references decoded and line ends normalized to "\n". A
// reference it does not recognize is copied as written.
func Unescape(dst, src []byte) []byte {
	for {
		i := bytes.IndexByte(src, '&')
		j := bytes.IndexByte(src, '\r')
		if i < 0 && j < 0 {
			return append(dst, src...)
		}
		if i < 0 || (j >= 0 && j < i) {
			dst = append(dst, src[:j]...)
			dst = append(dst, '\n')
			src = src[j+1:]
			if len(src) > 0 && src[0] == '\n' {
				src = src[1:]
			}
			continue
		}
		dst = append(dst, src[:i]...)
		src = src[i:]
		j = bytes.IndexByte(src, ';')
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
	switch {
	case Equal(ref, "lt"):
		return append(dst, '<'), true
	case Equal(ref, "gt"):
		return append(dst, '>'), true
	case Equal(ref, "amp"):
		return append(dst, '&'), true
	case Equal(ref, "apos"):
		return append(dst, '\''), true
	case Equal(ref, "quot"):
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

// pow10 holds the powers of ten a float64 represents exactly.
var pow10 = [...]float64{1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22}

// parseDecimalFast is Clinger's fast path: with a mantissa below 2^53 and a
// power of ten below 10^23, both operands of the division are exact and one
// IEEE operation rounds correctly, so the result is the one strconv would
// reach through its general scanner. It reports false for any input outside
// that shape, an exponent, a sign other than a leading minus, or no digits,
// and the caller falls back.
func parseDecimalFast(s []byte) (float64, bool) {
	i := 0
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		i = 1
	}
	var mant uint64
	sig, frac, nd := 0, 0, 0
	seenPoint := false
	for ; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			nd++
			if mant != 0 || c != '0' {
				if sig == 15 {
					return 0, false
				}
				sig++
			}
			mant = mant*10 + uint64(c-'0')
			if seenPoint {
				frac++
			}
			continue
		}
		if c == '.' && !seenPoint {
			seenPoint = true
			continue
		}
		return 0, false
	}
	if nd == 0 || frac >= len(pow10) {
		return 0, false
	}
	f := float64(mant)
	if frac > 0 {
		f /= pow10[frac]
	}
	if neg {
		f = -f
	}
	return f, true
}
