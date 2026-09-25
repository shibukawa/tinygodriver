package xmlro

import "iter"

// Err returns the error that stopped the reader, or nil. The iterators end
// silently on an error, so a loop over Tokens or Children checks Err after
// the loop, as a bufio.Scanner caller checks Scanner.Err.
func (r *Reader) Err() error { return r.err }

// Tokens iterates the remaining tokens, ending at EOF or at an error. The
// reader is the loop variable's context: inside the body Name, Attr, Text
// and the element-level calls all refer to the token just yielded.
//
//	for k := range r.Tokens() {
//		if k == xmlro.StartElement && r.NameIs("sheetData") {
//			r.Decode(&sheet)
//		}
//	}
//	if err := r.Err(); err != nil {
//		return err
//	}
func (r *Reader) Tokens() iter.Seq[Kind] {
	return func(yield func(Kind) bool) {
		for {
			k, err := r.Next()
			if err != nil || k == EOF {
				return
			}
			if !yield(k) {
				return
			}
		}
	}
}

// Children iterates the direct child elements of e, yielding each one's
// qualified name with the reader positioned on its StartElement. It is
// NextChild as a range loop, with the same skipping of whatever the body
// does not consume, and it ends silently on an error:
//
//	for name := range r.Children(r.Element()) {
//		switch {
//		case xmlro.Equal(name, "v"):
//			text, err := r.ElementText()
//			...
//		}
//	}
//
// The name is compared through Equal rather than string(name) because the
// conversion allocates under TinyGo; see Equal.
//
//	if err := r.Err(); err != nil {
//		return err
//	}
//
// A break leaves the reader on the child's StartElement; the element e is
// then still open, and NextChild or Skip finish it.
func (r *Reader) Children(e Element) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		for {
			ok, err := r.NextChild(e)
			if err != nil || !ok {
				return
			}
			if !yield(r.Name()) {
				return
			}
		}
	}
}
