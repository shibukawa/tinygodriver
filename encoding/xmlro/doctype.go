package xmlro

import "bytes"

// scanDoctype finds the end of the DOCTYPE at r.r and returns the offset of
// its closing '>' and the bounds of its internal subset, the bytes between
// '[' and ']', or -1 when there is none. Quoted literals, and inside the
// subset comments and processing instructions, are passed over so that a
// '>' or ']' within them does not end anything. Everything is relative to
// r.r, which a refill keeps.
func (r *Reader) scanDoctype() (end, subStart, subEnd int, err error) {
	i := 9
	var quote byte
	inSubset := false
	subStart, subEnd = -1, -1
	for {
		for i >= r.w-r.r {
			ok, err := r.more()
			if err != nil {
				return 0, 0, 0, err
			}
			if !ok {
				return 0, 0, 0, ErrTruncated
			}
		}
		c := r.buf[r.r+i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			i++
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
			i++
		case '[':
			if !inSubset && subStart < 0 {
				inSubset = true
				subStart = i + 1
			}
			i++
		case ']':
			if inSubset {
				inSubset = false
				subEnd = i
			}
			i++
		case '<':
			if !inSubset {
				i++
				continue
			}
			if ok, err := r.hasPrefixAt(i, "<!--"); err != nil {
				return 0, 0, 0, err
			} else if ok {
				j, err := r.index(i+4, "-->")
				if err != nil {
					return 0, 0, 0, err
				}
				if j < 0 {
					return 0, 0, 0, ErrTruncated
				}
				i = j + 3
				continue
			}
			if ok, err := r.hasPrefixAt(i, "<?"); err != nil {
				return 0, 0, 0, err
			} else if ok {
				j, err := r.index(i+2, "?>")
				if err != nil {
					return 0, 0, 0, err
				}
				if j < 0 {
					return 0, 0, 0, ErrTruncated
				}
				i = j + 2
				continue
			}
			// A markup declaration: scanned byte by byte, quotes honoured,
			// to its own '>'.
			i++
		case '>':
			if !inSubset {
				return i, subStart, subEnd, nil
			}
			i++
		default:
			i++
		}
	}
}

// declareEntities reads the general entities an internal subset declares
// in the internal form, <!ENTITY name "value">, and keeps them for the
// document. Parameter entities, external entities and every other
// declaration are passed over; nothing is fetched. A value's character
// references and predefined entities are decoded now, as XML decodes them
// in the literal; a reference to another general entity in it is kept as
// written and not expanded further.
func (r *Reader) declareEntities(sub []byte) {
	i := 0
	for i < len(sub) {
		rest := sub[i:]
		switch {
		case bytes.HasPrefix(rest, []byte("<!--")):
			j := bytes.Index(rest[4:], []byte("-->"))
			if j < 0 {
				return
			}
			i += 4 + j + 3
		case bytes.HasPrefix(rest, []byte("<?")):
			j := bytes.Index(rest[2:], []byte("?>"))
			if j < 0 {
				return
			}
			i += 2 + j + 2
		case bytes.HasPrefix(rest, []byte("<!ENTITY")) && len(rest) > 8 && isSpace(rest[8]):
			j := 8
			for j < len(rest) && isSpace(rest[j]) {
				j++
			}
			if j < len(rest) && rest[j] != '%' {
				ns := j
				for j < len(rest) && isEntityNameByte(rest[j]) {
					j++
				}
				name := rest[ns:j]
				for j < len(rest) && isSpace(rest[j]) {
					j++
				}
				if len(name) > 0 && j < len(rest) && (rest[j] == '"' || rest[j] == '\'') {
					q := rest[j]
					if k := bytes.IndexByte(rest[j+1:], q); k >= 0 {
						r.docEnts = append(r.docEnts, docEntity{name: string(name), value: string(Unescape(nil, rest[j+1:j+1+k]))})
						r.feat |= featEntities
					}
				}
			}
			i += skipDeclaration(rest)
		case rest[0] == '<':
			i += skipDeclaration(rest)
		default:
			i++
		}
	}
}

// skipDeclaration returns the length of the markup declaration at the start
// of b, up to and including its '>', with quoted literals passed over; or
// all of b when it is unterminated.
func skipDeclaration(b []byte) int {
	var quote byte
	for i := 1; i < len(b); i++ {
		c := b[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			return i + 1
		}
	}
	return len(b)
}
