// Package xml is a pull reader for XML documents shaped like the parts of an
// Office Open XML package: one encoding, no DTD, deep repetition of a few
// element shapes, and a caller that wants a handful of the elements and none
// of the rest.
//
// It is not a replacement for encoding/xml. It does not resolve namespaces,
// does not decode into arbitrary Go values, and does not accept a DOCTYPE. In
// exchange the reader allocates nothing in steady state: every name, attribute
// and text it returns is a slice into its own buffer, valid until the next
// call that advances the reader, and a caller copies only what it keeps.
//
// # Reading
//
// Next advances one token at a time. On a StartElement the caller reads the
// name and the attributes it wants, then either descends into the children,
// reads the text content with ElementText, captures the whole subtree with
// RawElement, or discards it with Skip. NextChild walks the direct children of
// an element and skips whatever the caller did not consume, so a decoder for
// one element is a loop over its children with a switch on the name:
//
//	el := r.Element()
//	for {
//		ok, err := r.NextChild(el)
//		if err != nil || !ok {
//			return err
//		}
//		switch {
//		case r.NameIs("v"):
//			text, err := r.ElementText()
//			...
//		case r.NameIs("is"):
//			err = inline.DecodeXMLFrom(r)
//		}
//	}
//
// # Names and namespaces
//
// Names are matched as written, prefix included: "w:p" is "w:p". Office XML
// generators emit canonical prefixes, so matching the qualified name is
// enough for that input and costs nothing. The xmlns attributes are visible
// through NextAttr for a caller that needs to resolve them.
//
// # Text
//
// Text and Attr return the bytes as written, entities included. A Value knows
// whether it holds any, and unescapes into a caller-owned buffer, so the
// common case of text with no ampersand is a slice and no copy.
//
// # Bounds
//
// The buffer grows only to hold one token, or one capture, and never past
// Options.MaxBufferBytes; nesting stops at Options.MaxDepth. A document that
// needs more is refused, not accommodated.
package xml
