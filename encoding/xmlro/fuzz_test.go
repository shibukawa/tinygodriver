//go:build !tinygo

package xmlro

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/shibukawa/tinygodriver/encoding/xmlro/htmlentity"
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

// htmlOptions is the reader for HTML-flavoured XML: every option that
// changes what the scanner accepts, with an identity charset hook so that a
// declaration naming another encoding exercises the switch.
func htmlOptions(bufferSize int) Options {
	return Options{
		BufferSize: bufferSize, MaxBufferBytes: 1 << 20, MaxDepth: 64,
		AllowDoctype: true, Lenient: true,
		Entities:      htmlentity.Lookup,
		AutoClose:     htmlentity.AutoClose,
		CharsetReader: func(string, io.Reader) (io.Reader, error) { return nil, nil },
	}
}

// identityCharset is a CharsetReader that keeps the bytes, standing in for a
// real converter.
func identityCharset(_ string, src io.Reader) (io.Reader, error) { return src, nil }

// The same property as FuzzReaderAgreesAcrossSources, with the tables, the
// lenient mode, the DOCTYPE subset, UTF-16 and the charset switch all on:
// none of them may make the byte-slice reader and the stream reader
// disagree, and none may panic.
func FuzzLenientReaderAgreesAcrossSources(f *testing.F) {
	for _, seed := range []string{
		`<a><b></a>`, `<a><b><c></a>`, `<a><b/></b></a>`, `<p>a<br>b<br></br><img src="x"><hr/>c</p>`,
		`<div>x &nbsp; &copy; &amp; &#65; &bogus; y</div>`, `<a b c=d e="f">t</a>`, `<a>&nbsp;<b>&eacute;</b></a>`,
		`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "x.dtd" [<!ENTITY ns_svg "http://www.w3.org/2000/svg"><!-- ] --><!ENTITY q "]&gt;">]><svg xmlns="&ns_svg;" a="&q;">&ns_svg;</svg>`,
		`<!DOCTYPE a [<!ENTITY % p "x"><!ENTITY e SYSTEM "y"><?pi ] ?>]><a>&e;</a>`,
		"\xFF\xFE<\x00a\x00>\x00\x34\xd8\x1e\xdd<\x00/\x00a\x00>\x00", "\xFE\xFF\x00<\x00a\x00/\x00>",
		`<?xml version="1.0" encoding="ISO-8859-1"?><a x="&eacute;">y</a>`, `<a>` + strings.Repeat("&nbsp;", 100) + `</a>`,
		`<svg><foreignObject><div>a<br>b</div></foreignObject></svg>`, "<!DOCTYPE a [", "<!DOCTYPE a [<!ENTITY", `<a b=`, `<p><BR>x</p>`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		o1 := htmlOptions(0)
		o1.CharsetReader = identityCharset
		o2 := htmlOptions(4)
		o2.CharsetReader = identityCharset
		got1, err1 := fuzzTokens(NewBytesReader(doc, o1))
		got2, err2 := fuzzTokens(NewReader(iotest.OneByteReader(bytes.NewReader(doc)), o2))
		if got1 != got2 || (err1 == nil) != (err2 == nil) {
			t.Errorf("sources disagree\nbytes:  %q %v\nstream: %q %v", got1, err1, got2, err2)
		}
	})
}

// structure is the element and text sequence of a document, the part of a
// token stream encoding/xml and this reader can be expected to agree on.
type structure struct {
	toks []tok
	err  error
}

// stdStructure reads doc through encoding/xml with Strict false, the HTML
// entity table and the HTML auto-close list.
func stdStructure(doc []byte) structure {
	d := xml.NewDecoder(bytes.NewReader(doc))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.AutoClose = xml.HTMLAutoClose
	var s structure
	for {
		tk, err := d.Token()
		if err == io.EOF {
			return s
		}
		if err != nil {
			s.err = err
			return s
		}
		switch v := tk.(type) {
		case xml.StartElement:
			s.toks = append(s.toks, tok{kind: StartElement, name: v.Name.Local})
		case xml.EndElement:
			s.toks = append(s.toks, tok{kind: EndElement, name: v.Name.Local})
		case xml.CharData:
			s.toks = append(s.toks, tok{kind: Text, text: string(v)})
		}
	}
}

// ourStructure reads doc through a Lenient reader with the same tables.
func ourStructure(doc []byte) structure {
	o := Options{MaxDepth: 1 << 16, Lenient: true, Entities: htmlentity.Lookup, AutoClose: htmlentity.AutoClose}
	r := NewBytesReader(doc, o)
	var s structure
	for {
		k, err := r.Next()
		if err != nil {
			s.err = err
			return s
		}
		switch k {
		case StartElement, EndElement:
			s.toks = append(s.toks, tok{kind: k, name: string(r.LocalName())})
		case Text:
			s.toks = append(s.toks, tok{kind: Text, text: r.Text().String()})
		case CData:
			// encoding/xml normalizes line ends inside CDATA too; this reader
			// hands CDATA back raw.
			s.toks = append(s.toks, tok{kind: Text, text: strings.ReplaceAll(strings.ReplaceAll(string(r.Text()), "\r\n", "\n"), "\r", "\n")})
		case EOF:
			return s
		}
	}
}

// comparable reports whether doc is inside the part of encoding/xml's
// non-strict grammar this reader claims: no directive but a DOCTYPE, which
// it refuses without AllowDoctype and encoding/xml reads as a Directive,
// no declaration naming an encoding, which is a CharsetReader question, and
// no byte order mark, since encoding/xml decodes neither UTF-16 nor skips
// the UTF-8 one, which this reader does.
func comparable(doc []byte) bool {
	if bytes.Contains(doc, []byte("<?")) || bytes.HasPrefix(doc, []byte("\xFF\xFE")) || bytes.HasPrefix(doc, []byte("\xFE\xFF")) || bytes.HasPrefix(doc, []byte("\xEF\xBB\xBF")) {
		return false
	}
	for i := 0; ; {
		j := bytes.Index(doc[i:], []byte("<!"))
		if j < 0 {
			return true
		}
		rest := doc[i+j:]
		if !bytes.HasPrefix(rest, []byte("<!--")) && !bytes.HasPrefix(rest, []byte("<![CDATA[")) {
			return false
		}
		i += j + 2
	}
}

// Where encoding/xml with Strict false reads a document to the end, a
// Lenient reader with the same tables must read the same elements and the
// same decoded text. Where encoding/xml stops with an error, the tokens it
// reported first must still be the first tokens this reader reports; this
// reader may go on, since it checks neither name productions nor the
// characters text may contain, which are most of what encoding/xml refuses.
// Names are compared without their prefix because encoding/xml reports them
// translated.
func FuzzLenientMatchesEncodingXML(f *testing.F) {
	for _, seed := range []string{
		`<a><b></a>`, `<a><b><c></a>`, `<a><b/></b></a>`, `<p>a<br>b<br></br><img src="x"><hr/>c</p>`,
		`<div>x &nbsp; &copy; &amp; &#65; &bogus; y</div>`, `<a b c=d e="f">t</a>`, `<a>&nbsp;<b>&eacute;</b></a>`,
		`<svg><foreignObject><div>a<br>b</div></foreignObject></svg>`, `<p><BR>x</p>`, `<a><![CDATA[x&amp;]]><!-- c --></a>`,
		`<a>x</A>`, `<a><b x="1"><c/>text</b><d/></a>`, `<br>`, `<a>&#xD800;&#x110000;&#65;</a>`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		if !comparable(doc) || !utf8.Valid(doc) {
			return
		}
		want := stdStructure(doc)
		got := ourStructure(doc)
		n := min(len(want.toks), len(got.toks))
		for i := range n {
			if want.toks[i] != got.toks[i] {
				t.Fatalf("token %d: got %+v, want %+v\nours: %+v %v\nstd:  %+v %v", i, got.toks[i], want.toks[i], got.toks, got.err, want.toks, want.err)
			}
		}
		if want.err != nil {
			if len(got.toks) < len(want.toks) {
				t.Fatalf("stopped before encoding/xml did\nours: %+v %v\nstd:  %+v %v", got.toks, got.err, want.toks, want.err)
			}
			return
		}
		if len(want.toks) != len(got.toks) || got.err != nil {
			t.Fatalf("ours: %+v %v\nstd:  %+v %v", got.toks, got.err, want.toks, want.err)
		}
	})
}
