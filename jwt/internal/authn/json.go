package authn

import (
	"bytes"
	"errors"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	ErrMalformedJSON = errors.New("authn: malformed JSON")
	ErrDuplicateJSON = errors.New("authn: duplicate JSON member")
)

// JSONOptions bounds a document: its size in bytes, how deeply values nest,
// and how many object members and array elements it holds in all.
type JSONOptions struct {
	MaxBytes   int
	MaxDepth   int
	MaxMembers int
}

// ValidateJSON reports whether data is exactly one JSON value within options,
// with no member name repeated within any object. The grammar is RFC 8259's;
// a string may carry any bytes above the control range, which is what
// encoding/json accepts too.
//
// This is a hand-written scanner rather than encoding/json's token stream,
// which boxed every token, kept a set of names per object, and so allocated
// some thirty times to validate a token's two small objects.
func ValidateJSON(data []byte, options JSONOptions) error {
	if err := checkJSONOptions(data, options); err != nil {
		return err
	}
	s := jsonScanner{data: data, opts: options}
	if err := s.value(1); err != nil {
		return err
	}
	return s.end()
}

// ObjectMembers validates data as ValidateJSON does, requires its value to be
// an object, and hands that object's members to visit in document order: the
// name decoded, the value as the bytes of the document that spell it. Each
// value has been validated before visit sees it. A nested object's members
// are not visited.
//
// Both slices are views of data, except a name that had to be decoded, which
// is its own copy. A visitor that keeps the value keeps data alive.
func ObjectMembers(data []byte, options JSONOptions, visit func(name, value []byte) error) error {
	if err := checkJSONOptions(data, options); err != nil {
		return err
	}
	s := jsonScanner{data: data, opts: options}
	s.skipSpace()
	if s.pos >= len(s.data) || s.data[s.pos] != '{' {
		return ErrMalformedJSON
	}
	s.pos++
	if err := s.object(1, visit, data); err != nil {
		return err
	}
	return s.end()
}

// ArrayElements hands each element of value, which must be a JSON array that
// ValidateJSON or ObjectMembers has accepted, to visit as the bytes that
// spell it.
func ArrayElements(value []byte, visit func(elem []byte) error) error {
	// The array was validated under some bound already; what is left to
	// enforce is the shape, so the bounds here are the input's own size.
	s := jsonScanner{data: value, opts: JSONOptions{MaxBytes: len(value), MaxDepth: len(value) + 1, MaxMembers: len(value) + 1}}
	s.skipSpace()
	if s.pos >= len(s.data) || s.data[s.pos] != '[' {
		return ErrMalformedJSON
	}
	s.pos++
	if err := s.array(1, visit, value); err != nil {
		return err
	}
	return s.end()
}

// Unquote decodes a JSON string token, quotes included, that ValidateJSON or
// ObjectMembers has accepted. Escapes are resolved as encoding/json resolves
// them: a surrogate pair becomes one character, a lone surrogate and an
// invalid UTF-8 sequence become U+FFFD.
func Unquote(raw []byte) (string, bool) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", false
	}
	body := raw[1 : len(raw)-1]
	if bytes.IndexByte(body, '\\') < 0 && utf8.Valid(body) {
		return string(body), true
	}
	var buf [128]byte
	return string(appendUnescaped(buf[:0], body)), true
}

func checkJSONOptions(data []byte, options JSONOptions) error {
	if options.MaxBytes <= 0 || options.MaxDepth <= 0 || options.MaxMembers <= 0 {
		return ErrInvalidSize
	}
	if len(data) > options.MaxBytes {
		return ErrLimitExceeded
	}
	return nil
}

// maxScannedKeys is the most names the scanner records at once, across every
// object it is inside, where each new name is checked against its object's
// recorded ones by hash and then bytes. An object whose names do not fit moves
// them into a map, which allocates per name but keeps a large object linear;
// an object that large is already an unusual token.
const maxScannedKeys = 64

// jsonKey locates one recorded member name: a range of the document, or of
// the scanner's arena when the name had to be decoded first.
type jsonKey struct {
	off, end int
	arena    bool
	hash     uint32
}

func (k jsonKey) bytes(s *jsonScanner) []byte {
	if k.arena {
		return s.arena[k.off:k.end]
	}
	return s.data[k.off:k.end]
}

// jsonScanner walks one document. keys holds the names of every object the
// scan is inside, innermost last, so that a name is checked only against its
// own object's; arena holds the decoded form of the names that had escapes or
// invalid UTF-8, since two spellings of one name are the same member.
//
// The scanner lives in its caller's frame, keys included, which is what makes
// validating a token allocate nothing. Two things keep it there. keys is an
// array written by index, because appending to a slice held in a field sends
// the field's backing to the heap. And what reaches a visitor is sliced from
// the document passed down as a parameter, never from the scanner's fields:
// escape analysis does not tell one field from another, so a slice of s.data
// handed to an unknown function would send everything the scanner points at
// to the heap.
type jsonScanner struct {
	data    []byte
	pos     int
	opts    JSONOptions
	members int
	nkeys   int
	keys    [maxScannedKeys]jsonKey
	arena   []byte
}

// end requires that nothing but whitespace follows the value.
func (s *jsonScanner) end() error {
	s.skipSpace()
	if s.pos != len(s.data) {
		return ErrMalformedJSON
	}
	return nil
}

func (s *jsonScanner) skipSpace() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

// value scans one value. depth counts the top-level value as 1 and every
// value a container holds as one more, which is the depth the limit bounds.
func (s *jsonScanner) value(depth int) error {
	if depth > s.opts.MaxDepth {
		return ErrLimitExceeded
	}
	s.skipSpace()
	if s.pos >= len(s.data) {
		return ErrMalformedJSON
	}
	switch c := s.data[s.pos]; {
	case c == '{':
		s.pos++
		return s.object(depth, nil, nil)
	case c == '[':
		s.pos++
		return s.array(depth, nil, nil)
	case c == '"':
		_, err := s.str()
		return err
	case c == '-' || ('0' <= c && c <= '9'):
		return s.number()
	case c == 't':
		return s.literal("true")
	case c == 'f':
		return s.literal("false")
	case c == 'n':
		return s.literal("null")
	}
	return ErrMalformedJSON
}

// object scans the members of an object whose opening brace is consumed.
// doc is the document for the visitor's slices; see jsonScanner.
func (s *jsonScanner) object(depth int, visit func(name, value []byte) error, doc []byte) error {
	base := s.nkeys
	var large map[string]struct{}
	s.skipSpace()
	if s.pos < len(s.data) && s.data[s.pos] == '}' {
		s.pos++
		return nil
	}
	for {
		s.skipSpace()
		if s.pos >= len(s.data) || s.data[s.pos] != '"' {
			return ErrMalformedJSON
		}
		key, err := s.key()
		if err != nil {
			return err
		}
		s.members++
		if s.members > s.opts.MaxMembers {
			return ErrLimitExceeded
		}
		if err := s.note(base, &large, key); err != nil {
			return err
		}
		// The value's depth is checked before its separator is, so a member
		// that is both too deep and malformed reports the limit, as it always
		// has.
		if depth+1 > s.opts.MaxDepth {
			return ErrLimitExceeded
		}
		s.skipSpace()
		if s.pos >= len(s.data) || s.data[s.pos] != ':' {
			return ErrMalformedJSON
		}
		s.pos++
		s.skipSpace()
		start := s.pos
		if err := s.value(depth + 1); err != nil {
			return err
		}
		if visit != nil {
			var name []byte
			if key.arena {
				name = append([]byte(nil), s.arena[key.off:key.end]...)
			} else {
				name = doc[key.off:key.end]
			}
			if err := visit(name, doc[start:s.pos]); err != nil {
				return err
			}
		}
		s.skipSpace()
		if s.pos >= len(s.data) {
			return ErrMalformedJSON
		}
		c := s.data[s.pos]
		s.pos++
		if c == '}' {
			s.nkeys = base
			return nil
		}
		if c != ',' {
			return ErrMalformedJSON
		}
	}
}

// array scans the elements of an array whose opening bracket is consumed.
// doc is the document for the visitor's slices; see jsonScanner.
func (s *jsonScanner) array(depth int, visit func(elem []byte) error, doc []byte) error {
	s.skipSpace()
	if s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ']':
			s.pos++
			return nil
		case '}':
			return ErrMalformedJSON
		}
	}
	for first := true; ; first = false {
		// Counted and bounded before its separator, or its absence, is
		// checked, so an element that is one too many, or too deep, reports
		// the limit whatever follows the one before it; that is the order the
		// token-stream validator kept, whose More looked only for a closing
		// bracket.
		s.members++
		if s.members > s.opts.MaxMembers {
			return ErrLimitExceeded
		}
		if depth+1 > s.opts.MaxDepth {
			return ErrLimitExceeded
		}
		if s.pos >= len(s.data) {
			return ErrMalformedJSON
		}
		if !first {
			if s.data[s.pos] != ',' {
				return ErrMalformedJSON
			}
			s.pos++
		}
		s.skipSpace()
		start := s.pos
		if err := s.value(depth + 1); err != nil {
			return err
		}
		if visit != nil {
			if err := visit(doc[start:s.pos]); err != nil {
				return err
			}
		}
		s.skipSpace()
		if s.pos < len(s.data) {
			switch s.data[s.pos] {
			case ']':
				s.pos++
				return nil
			case '}':
				return ErrMalformedJSON
			}
		}
	}
}

// key scans a member name and records where its decoded form lives. A name
// spelled without escapes in valid UTF-8 is its own decoded form.
func (s *jsonScanner) key() (jsonKey, error) {
	start := s.pos + 1
	escaped, err := s.str()
	if err != nil {
		return jsonKey{}, err
	}
	raw := s.data[start : s.pos-1]
	if !escaped && utf8.Valid(raw) {
		return jsonKey{off: start, end: s.pos - 1, hash: hashBytes(raw)}, nil
	}
	off := len(s.arena)
	s.arena = appendUnescaped(s.arena, raw)
	return jsonKey{off: off, end: len(s.arena), arena: true, hash: hashBytes(s.arena[off:])}, nil
}

// note records key for the object whose names start at base in s.keys,
// reporting a duplicate against the names already recorded there.
func (s *jsonScanner) note(base int, large *map[string]struct{}, key jsonKey) error {
	name := key.bytes(s)
	if *large != nil {
		if _, dup := (*large)[string(name)]; dup {
			return ErrDuplicateJSON
		}
		(*large)[string(name)] = struct{}{}
		return nil
	}
	recorded := s.keys[base:s.nkeys]
	for _, r := range recorded {
		if r.hash == key.hash && bytes.Equal(r.bytes(s), name) {
			return ErrDuplicateJSON
		}
	}
	if s.nkeys < maxScannedKeys {
		s.keys[s.nkeys] = key
		s.nkeys++
		return nil
	}
	m := make(map[string]struct{}, 2*maxScannedKeys)
	for _, r := range recorded {
		m[string(r.bytes(s))] = struct{}{}
	}
	m[string(name)] = struct{}{}
	*large = m
	s.nkeys = base
	return nil
}

// str scans a string whose opening quote is at s.pos, leaving s.pos past the
// closing one, and reports whether it held an escape.
func (s *jsonScanner) str() (bool, error) {
	s.pos++
	escaped := false
	for s.pos < len(s.data) {
		c := s.data[s.pos]
		switch {
		case c == '"':
			s.pos++
			return escaped, nil
		case c == '\\':
			escaped = true
			s.pos++
			if s.pos >= len(s.data) {
				return false, ErrMalformedJSON
			}
			switch s.data[s.pos] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				s.pos++
			case 'u':
				if s.pos+4 >= len(s.data) {
					return false, ErrMalformedJSON
				}
				for _, h := range s.data[s.pos+1 : s.pos+5] {
					if hexValue(h) < 0 {
						return false, ErrMalformedJSON
					}
				}
				s.pos += 5
			default:
				return false, ErrMalformedJSON
			}
		case c < 0x20:
			return false, ErrMalformedJSON
		default:
			s.pos++
		}
	}
	return false, ErrMalformedJSON
}

func (s *jsonScanner) number() error {
	d, p := s.data, s.pos
	if d[p] == '-' {
		p++
	}
	switch {
	case p >= len(d):
		return ErrMalformedJSON
	case d[p] == '0':
		p++
	case '1' <= d[p] && d[p] <= '9':
		for p < len(d) && isDigit(d[p]) {
			p++
		}
	default:
		return ErrMalformedJSON
	}
	if p < len(d) && d[p] == '.' {
		p++
		if p >= len(d) || !isDigit(d[p]) {
			return ErrMalformedJSON
		}
		for p < len(d) && isDigit(d[p]) {
			p++
		}
	}
	if p < len(d) && (d[p] == 'e' || d[p] == 'E') {
		p++
		if p < len(d) && (d[p] == '+' || d[p] == '-') {
			p++
		}
		if p >= len(d) || !isDigit(d[p]) {
			return ErrMalformedJSON
		}
		for p < len(d) && isDigit(d[p]) {
			p++
		}
	}
	s.pos = p
	return nil
}

func (s *jsonScanner) literal(lit string) error {
	if !bytes.HasPrefix(s.data[s.pos:], []byte(lit)) {
		return ErrMalformedJSON
	}
	s.pos += len(lit)
	return nil
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func hexValue(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c && c <= 'f':
		return int(c-'a') + 10
	case 'A' <= c && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// hashBytes is FNV-1a, which is enough to keep a name scan to one compare per
// genuine match.
func hashBytes(b []byte) uint32 {
	h := uint32(2166136261)
	for _, c := range b {
		h = (h ^ uint32(c)) * 16777619
	}
	return h
}

// appendUnescaped appends the decoded form of body, the inside of a string
// token that str has accepted, to dst.
func appendUnescaped(dst, body []byte) []byte {
	for i := 0; i < len(body); {
		c := body[i]
		switch {
		case c == '\\':
			i++
			switch body[i] {
			case '"', '\\', '/':
				dst = append(dst, body[i])
				i++
			case 'b':
				dst = append(dst, '\b')
				i++
			case 'f':
				dst = append(dst, '\f')
				i++
			case 'n':
				dst = append(dst, '\n')
				i++
			case 'r':
				dst = append(dst, '\r')
				i++
			case 't':
				dst = append(dst, '\t')
				i++
			case 'u':
				r := hex4(body[i+1:])
				i += 5
				if utf16.IsSurrogate(r) {
					r2 := rune(utf8.RuneError)
					if i+6 <= len(body) && body[i] == '\\' && body[i+1] == 'u' {
						r2 = hex4(body[i+2:])
					}
					if dec := utf16.DecodeRune(r, r2); dec != utf8.RuneError {
						r = dec
						i += 6
					} else {
						r = utf8.RuneError
					}
				}
				dst = utf8.AppendRune(dst, r)
			}
		case c < utf8.RuneSelf:
			dst = append(dst, c)
			i++
		default:
			r, size := utf8.DecodeRune(body[i:])
			if r == utf8.RuneError && size == 1 {
				dst = utf8.AppendRune(dst, utf8.RuneError)
			} else {
				dst = append(dst, body[i:i+size]...)
			}
			i += size
		}
	}
	return dst
}

// hex4 reads four hex digits that str has already checked.
func hex4(b []byte) rune {
	var r rune
	for _, c := range b[:4] {
		r = r<<4 | rune(hexValue(c))
	}
	return r
}
