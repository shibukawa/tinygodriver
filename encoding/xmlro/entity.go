package xmlro

import (
	"bytes"
	"strings"
)

// docEntity is a general entity the DOCTYPE's internal subset declared, with
// its value decoded.
type docEntity struct {
	name  string
	value string
}

// lookupEntity resolves name through the DOCTYPE's declarations, the first
// of which wins as XML requires, and then through Options.Entities.
func (r *Reader) lookupEntity(name []byte) (string, bool) {
	for i := range r.docEnts {
		if Equal(name, r.docEnts[i].name) {
			return r.docEnts[i].value, true
		}
	}
	if r.opts.Entities != nil {
		return r.opts.Entities(name)
	}
	return "", false
}

// maxEntityName bounds the name of a reference the tables are asked about.
// HTML's longest is 31 bytes; Illustrator's namespace entities are under 16.
const maxEntityName = 64

// isEntityNameByte marks the bytes an entity name may contain: the ASCII
// name characters and every non-ASCII byte, looser than the production, as
// nameByte is.
func isEntityNameByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-' || c == '.' || c == ':' || c >= 0x80
}

// isPredefined reports whether name is one of the five references XML
// predefines, which Unescape decodes and the tables are never asked about.
func isPredefined(name []byte) bool {
	switch len(name) {
	case 2:
		return Equal(name, "lt") || Equal(name, "gt")
	case 3:
		return Equal(name, "amp")
	case 4:
		return Equal(name, "apos") || Equal(name, "quot")
	}
	return false
}

// expand appends src to dst with every reference a table resolves replaced
// by its value, and reports whether any was. The five predefined references
// and character references are left for Unescape, and a '&' or '\r' in a
// value is written as a reference, so that decoding the result yields the
// value itself. The result is bounded by MaxBufferBytes: no declaration can
// amplify a document past the reader's other bounds.
func (r *Reader) expand(dst, src []byte) ([]byte, bool, error) {
	changed := false
	for {
		i := bytes.IndexByte(src, '&')
		if i < 0 {
			break
		}
		dst = append(dst, src[:i]...)
		src = src[i:]
		// The name runs from the '&' to a ';' within the bound.
		j := 1
		for j < len(src) && j <= maxEntityName && isEntityNameByte(src[j]) {
			j++
		}
		if j == 1 || j >= len(src) || src[j] != ';' || isPredefined(src[1:j]) {
			dst = append(dst, '&')
			src = src[1:]
			continue
		}
		if v, ok := r.lookupEntity(src[1:j]); ok {
			dst = appendEscaped(dst, v)
			changed = true
			if len(dst) > r.opts.MaxBufferBytes {
				return dst, changed, ErrTooLarge
			}
		} else {
			dst = append(dst, src[:j+1]...)
		}
		src = src[j+1:]
	}
	return append(dst, src...), changed, nil
}

// appendEscaped appends v with the two bytes decoding treats specially
// written as references.
func appendEscaped(dst []byte, v string) []byte {
	if strings.IndexByte(v, '&') < 0 && strings.IndexByte(v, '\r') < 0 {
		return append(dst, v...)
	}
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '&':
			dst = append(dst, "&amp;"...)
		case '\r':
			dst = append(dst, "&#13;"...)
		default:
			dst = append(dst, v[i])
		}
	}
	return dst
}

// expandText rewrites the Text token just scanned through the tables, when
// one of them resolves a reference in it. The rewritten text lives in alt
// and Text returns it in place of the buffer's bytes.
func (r *Reader) expandText() error {
	start := len(r.alt)
	out, changed, err := r.expand(r.alt, r.buf[r.textOff:r.textOff+r.textLen])
	if err != nil {
		return err
	}
	if !changed {
		r.alt = out[:start]
		return nil
	}
	r.alt = out
	r.textAlt = r.alt[start:]
	r.flags |= flagDirty
	return nil
}

// expandAttrs rewrites the attribute values of the start tag just scanned
// through the tables, those a table changes, into alt.
func (r *Reader) expandAttrs() error {
	for i := range r.attrs {
		e := &r.attrs[i]
		v := r.attrValue(*e)
		if bytes.IndexByte(v, '&') < 0 {
			continue
		}
		start := len(r.alt)
		out, changed, err := r.expand(r.alt, v)
		if err != nil {
			return err
		}
		if !changed {
			r.alt = out[:start]
			continue
		}
		r.alt = out
		r.flags |= flagDirty
		e.valOff = -int32(start) - 1
		e.valLen = int32(len(out) - start)
	}
	return nil
}
