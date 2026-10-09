// Package htmlentity is the HTML vocabulary an xmlro.Reader needs to tokenize
// HTML-flavoured XML: the named character references of HTML 4 and the
// elements that end with their start tag. It is a separate package so that a
// reader of Office parts, which needs neither, links neither table.
//
//	r := xmlro.NewReader(src, xmlro.Options{
//		Entities:  htmlentity.Lookup,
//		AutoClose: htmlentity.AutoClose,
//		Lenient:   true,
//	})
//
// is the reader encoding/xml builds from Strict false, Entity = xml.HTMLEntity
// and AutoClose = xml.HTMLAutoClose, and a document reads the same through
// both.
package htmlentity

import (
	"bytes"
	"unsafe"
)

type entry struct {
	name, value string
}

// Lookup resolves one of the 252 named character references of HTML 4, the
// set encoding/xml.HTMLEntity holds, by the name between & and ;. It is an
// xmlro.Options.Entities, allocates nothing, and does not know the five
// references XML predefines, which the reader resolves before asking.
func Lookup(name []byte) (string, bool) {
	// A string view of the bytes, for comparisons that allocate on neither
	// compiler; it does not outlive the call.
	s := unsafe.String(unsafe.SliceData(name), len(name))
	lo, hi := 0, len(table)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		switch e := &table[m]; {
		case e.name < s:
			lo = m + 1
		case e.name > s:
			hi = m
		default:
			return e.value, true
		}
	}
	return "", false
}

// voidElements are the elements encoding/xml.HTMLAutoClose names: those HTML
// ends at their start tag.
var voidElements = [...]string{"area", "base", "basefont", "br", "col", "frame", "hr", "img", "input", "isindex", "link", "meta", "param"}

// AutoClose reports whether name is an HTML element that ends with its start
// tag: br, img, hr and the rest of encoding/xml.HTMLAutoClose. The
// comparison ignores ASCII case and a namespace prefix, as encoding/xml's
// does, so xhtml:br inside an SVG foreignObject is one too. A colon that is
// the first or the last byte is part of the name, not a prefix, as
// encoding/xml also has it. It is an xmlro.Options.AutoClose.
func AutoClose(name []byte) bool {
	if i := bytes.IndexByte(name, ':'); i > 0 && i < len(name)-1 {
		name = name[i+1:]
	}
	if len(name) < 2 || len(name) > 8 {
		return false
	}
	for _, v := range voidElements {
		if equalFold(name, v) {
			return true
		}
	}
	return false
}

// equalFold compares b to the lower-case ASCII s, folding b's case.
func equalFold(b []byte, s string) bool {
	if len(b) != len(s) {
		return false
	}
	for i := range b {
		c := b[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != s[i] {
			return false
		}
	}
	return true
}
