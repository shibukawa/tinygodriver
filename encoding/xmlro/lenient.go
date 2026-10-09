package xmlro

// pushName keeps the name of the element just opened, so that Lenient can
// name the EndElement it may have to invent for it. Only a Lenient reader
// keeps names; the others keep the hash alone.
func (r *Reader) pushName(name []byte) {
	r.names = append(r.names, name...)
	r.nameEnds = append(r.nameEnds, int32(len(r.names)))
}

// openName returns the name of the innermost open element, which Lenient
// kept.
func (r *Reader) openName() []byte {
	start := 0
	if r.depth > 1 {
		start = int(r.nameEnds[r.depth-2])
	}
	return r.names[start:r.nameEnds[r.depth-1]]
}

// popElement closes the innermost open element. The two hot sites, next
// and scanEndTag, carry the same four lines themselves because the inliner
// will not take this body with a call in it.
func (r *Reader) popElement() {
	r.depth--
	r.stack = r.stack[:r.depth]
	if len(r.nameEnds)+len(r.ns) > 0 {
		r.popKept()
	}
}

// popKept drops what else was kept for the element just closed: the name a
// Lenient reader holds, and the namespace declarations it made.
func (r *Reader) popKept() {
	if len(r.nameEnds) > 0 {
		r.nameEnds = r.nameEnds[:r.depth]
		end := 0
		if r.depth > 0 {
			end = int(r.nameEnds[r.depth-1])
		}
		r.names = r.names[:end]
	}
	if len(r.ns) > 0 {
		r.popNamespaces()
	}
}

// beforeToken handles what flags say is pending before the next token is
// scanned: an end tag still being unwound or a charset to switch to, either
// of which may produce the token itself, and the previous token's rewritten
// text, names and values to let go of.
func (r *Reader) beforeToken() (Kind, bool, error) {
	if r.flags&flagUnwinding != 0 {
		k, err := r.unwind()
		return k, true, err
	}
	if r.flags&flagCharset != 0 {
		r.flags &^= flagCharset
		if err := r.switchCharset(); err != nil {
			return None, true, err
		}
	}
	if r.flags&flagDirty != 0 {
		r.flags &^= flagDirty
		r.synthName, r.textAlt = nil, nil
		r.alt = r.alt[:0]
	}
	return None, false, nil
}

// mismatchedEndTag is the end of scanEndTag for a tag whose name is not the
// open element's: a SyntaxError, or under Lenient the start of unwinding.
func (r *Reader) mismatchedEndTag(h uint32, end, i int) (Kind, error) {
	if !r.opts.Lenient {
		return None, r.syntax(0, "end tag does not match open element")
	}
	// End the elements this tag leaves open, one per call, and consume the
	// tag when its element is reached.
	r.closeHash, r.closeLen, r.closeAdvance = h, end-2, i+1
	r.flags |= flagUnwinding
	return r.unwind()
}

// unwind is Lenient's answer to an end tag that does not match the open
// element: each call ends the innermost open element, named from the names
// Lenient kept, until the element the tag names is on top and the tag ends
// it, or no element is left and the tag is an error. The input does not
// move until the tag is consumed, so Offset stays at the tag throughout.
// This is the sequence encoding/xml reports with Strict false: a stray end
// tag closes everything, then fails.
func (r *Reader) unwind() (Kind, error) {
	if r.depth == 0 {
		r.flags &^= flagUnwinding
		return None, r.syntax(0, "unexpected end element")
	}
	if r.stack[r.depth-1] == r.closeHash {
		r.flags &^= flagUnwinding
		r.synthName = nil
		r.nameOff, r.nameLen = r.r+2, r.closeLen
		r.r += r.closeAdvance
		r.popElement()
		return EndElement, nil
	}
	r.synthName = r.openName()
	r.flags |= flagDirty
	r.popElement()
	return EndElement, nil
}

// scanUnquotedValue scans an unquoted attribute value starting at rel, the
// bytes encoding/xml allows in one with Strict false, and returns the offset
// after it.
func (r *Reader) scanUnquotedValue(rel int) (int, error) {
	for {
		if r.r+rel >= r.w {
			ok, err := r.more()
			if err != nil {
				return 0, err
			}
			if !ok {
				return 0, ErrTruncated
			}
			continue
		}
		if !unquotedByte[r.buf[r.r+rel]] {
			return rel, nil
		}
		rel++
	}
}

// skipTokens is Skip for a reader whose documents may need end tags
// invented, Lenient or AutoClose, where the raw scan cannot know where the
// element ends.
func (r *Reader) skipTokens() error {
	want := r.depth - 1
	for {
		k, err := r.Next()
		if err != nil {
			return err
		}
		if k == EndElement && r.depth == want {
			return nil
		}
	}
}

// shouldAutoClose reports whether the void element whose start tag just
// ended at rel is to be closed now. It is, unless its own end tag is the
// next token, or the input ends where that end tag could still be: then
// the element stays open and the truncation is reported, as encoding/xml
// reports it, rather than an invented end. The start tag is pinned so that
// reading ahead keeps it in the buffer.
func (r *Reader) shouldAutoClose(rel, nameLen int) (bool, error) {
	saved := r.pin
	if saved < 0 || saved > r.tokStart {
		r.pin = r.tokStart
	}
	close, err := r.peekNotEndTag(rel, nameLen)
	r.pin = saved
	return close, err
}

// peekNotEndTag compares the input after rel with "</", the element's own
// name under ASCII case folding, optional space and '>', and reports true
// at the first byte that differs; false when the input ends first or the
// end tag is complete.
func (r *Reader) peekNotEndTag(rel, nameLen int) (bool, error) {
	for j := 0; ; j++ {
		ok, err := r.avail(rel + j)
		if err != nil || !ok {
			return false, err
		}
		c := r.buf[r.r+rel+j]
		switch {
		case j == 0:
			if c != '<' {
				return true, nil
			}
		case j == 1:
			if c != '/' {
				return true, nil
			}
		case j < 2+nameLen:
			if !asciiEqualFold(r.buf[r.r+rel+j:r.r+rel+j+1], r.buf[r.r+j-1:r.r+j]) {
				return true, nil
			}
		default:
			if c == '>' {
				return false, nil
			}
			if !isSpace(c) {
				return true, nil
			}
		}
	}
}

// asciiEqualFold compares two byte strings of equal length under ASCII case
// folding.
func asciiEqualFold(a, b []byte) bool {
	for i := range a {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// unquotedByte marks the bytes an unquoted attribute value may contain in
// a Lenient reader: the set encoding/xml allows with Strict false.
var unquotedByte = func() (t [256]bool) {
	for c := range 256 {
		t[c] = 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == ':' || c == '-'
	}
	return
}()
