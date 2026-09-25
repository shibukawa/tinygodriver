//go:build !tinygo

package xml

import (
	"bytes"
	"strings"
	"testing"
	"testing/iotest"
)

// The properties fuzzed: no input makes the reader panic, and the same
// document reads identically whether it arrives as one byte slice or one
// byte at a time through a buffer that starts too small for any token.
func FuzzReaderAgreesAcrossSources(f *testing.F) {
	for _, seed := range []string{
		`<a/>`, `<a b="1" c='2'><d>t &amp; &#65; &#x42;</d><!-- c --><![CDATA[x]]><?pi x?></a>`,
		"\xEF\xBB\xBF<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<w:p xmlns:w=\"u\"><w:r><w:t xml:space=\"preserve\"> a </w:t></w:r></w:p>",
		`<a><b></a>`, `<a x=1/>`, `<!DOCTYPE a><a/>`, `<a>` + strings.Repeat("x", 300) + `</a>`,
		`<a><b/><c>d</c><e f="g"/></a>`, "\xFF\xFE", "<", "</", "<!", "<!-", "<![CDATA[", "<?", "<a b=\"", "<a b",
		`<r><c r="A1" s="1" t="s"><v>0</v></c><c r="B1"><v>12.5</v></c></r>`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		got1, err1 := fuzzTokens(NewBytesReader(doc, Options{MaxDepth: 64}))
		got2, err2 := fuzzTokens(NewReader(iotest.OneByteReader(bytes.NewReader(doc)), Options{BufferSize: 4, MaxBufferBytes: 1 << 20, MaxDepth: 64}))
		if got1 != got2 || (err1 == nil) != (err2 == nil) {
			t.Errorf("sources disagree\nbytes:  %q %v\nstream: %q %v", got1, err1, got2, err2)
		}
	})
}

// fuzzTokens serializes what the reader saw, exercising every accessor
// along the way, and stops at the first error.
func fuzzTokens(r *Reader) (string, error) {
	var b strings.Builder
	for {
		k, err := r.Next()
		if err != nil {
			return b.String(), err
		}
		b.WriteString(k.String())
		switch k {
		case StartElement:
			b.Write(r.Name())
			b.WriteString("|")
			b.WriteString(r.Namespace())
			for {
				n, v, ok := r.NextAttr()
				if !ok {
					break
				}
				b.WriteString(" ")
				b.Write(n)
				b.WriteString("=")
				b.WriteString(v.String())
			}
		case EndElement, ProcInst:
			b.Write(r.Name())
		}
		switch k {
		case Text, CData, Comment, ProcInst, Directive:
			b.WriteString(r.Text().String())
		}
		b.WriteString(";")
		if k == EOF {
			return b.String(), nil
		}
	}
}

// Every element-level call, on every element, must also agree across
// sources and never panic.
func FuzzElementCalls(f *testing.F) {
	for _, seed := range []string{
		`<a><b x="1"><c/>text</b><d/></a>`, `<a>x<b>y</b>z<![CDATA[w]]></a>`, `<a><b><c><d/></c></b></a>`, `<a`,
	} {
		f.Add([]byte(seed), uint8(0))
	}
	f.Fuzz(func(t *testing.T, doc []byte, mode uint8) {
		run := func(r *Reader) string {
			var b strings.Builder
			for k := range r.Tokens() {
				if k != StartElement {
					continue
				}
				switch mode % 4 {
				case 0:
					v, err := r.ElementText()
					b.WriteString(v.String())
					b.WriteString(errString(err))
				case 1:
					raw, err := r.RawElement()
					b.Write(raw)
					b.WriteString(errString(err))
				case 2:
					b.WriteString(errString(r.Skip()))
				case 3:
					for name := range r.Children(r.Element()) {
						b.Write(name)
					}
				}
				b.WriteString(";")
			}
			b.WriteString(errString(r.Err()))
			return b.String()
		}
		got1 := run(NewBytesReader(doc, Options{MaxDepth: 64}))
		got2 := run(NewReader(iotest.OneByteReader(bytes.NewReader(doc)), Options{BufferSize: 4, MaxBufferBytes: 1 << 20, MaxDepth: 64}))
		if got1 != got2 {
			t.Errorf("mode %d: sources disagree\nbytes:  %q\nstream: %q", mode%4, got1, got2)
		}
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return "<err>"
}

func FuzzUnescapeNeverPanics(f *testing.F) {
	for _, s := range []string{"", "&", "&amp;", "&#x;", "&#99999999999;", "a\r\nb", "&#xD800;"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		v := Value(in)
		out := v.String()
		if !v.Equal(out) {
			t.Errorf("Equal disagrees with String for %q", in)
		}
		if !v.HasEntities() && out != string(in) {
			t.Errorf("HasEntities false but decoding changed %q", in)
		}
	})
}
