//go:build !tinygo

package xmlro

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/shibukawa/tinygodriver/encoding/xmlro/htmlentity"
)

// stdTokens is the (kind, name, decoded text) sequence encoding/xml reports
// for doc with Strict false, the HTML entity table and the HTML auto-close
// list: what a Lenient reader with the same tables must reproduce.
func stdTokens(t *testing.T, doc string) []tok {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(doc))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.AutoClose = xml.HTMLAutoClose
	var out []tok
	for {
		tk, err := d.Token()
		if err == io.EOF {
			return append(out, tok{kind: EOF})
		}
		if err != nil {
			return out
		}
		switch v := tk.(type) {
		case xml.StartElement:
			out = append(out, tok{kind: StartElement, name: v.Name.Local})
		case xml.EndElement:
			out = append(out, tok{kind: EndElement, name: v.Name.Local})
		case xml.CharData:
			out = append(out, tok{kind: Text, text: string(v)})
		}
	}
}

func htmlTokens(r *Reader) []tok {
	var out []tok
	for {
		k, err := r.Next()
		if err != nil {
			return out
		}
		switch k {
		case StartElement, EndElement:
			out = append(out, tok{kind: k, name: string(r.LocalName())})
		case Text:
			out = append(out, tok{kind: k, text: r.Text().String()})
		case EOF:
			return append(out, tok{kind: EOF})
		}
	}
}

func TestLenientMatchesEncodingXMLNonStrict(t *testing.T) {
	docs := []string{
		`<a><b></a>`, `<a><b><c></a>`, `<a><b/></b></a>`, `<p>a<br>b<br></br><img src="x"><hr/>c</p>`,
		`<div>x &nbsp; &copy; &amp; &#65; &bogus; y</div>`, `<a b c=d e="f">t</a>`, `<a>&nbsp;<b>&eacute;</b></a>`,
		`<p><BR>x</p>`, `<svg><foreignObject><div>a<br>b</div></foreignObject></svg>`,
	}
	opts := Options{Lenient: true, Entities: htmlentity.Lookup, AutoClose: htmlentity.AutoClose}
	for _, doc := range docs {
		want := stdTokens(t, doc)
		for _, got := range [][]tok{htmlTokens(NewBytesReader([]byte(doc), opts)), htmlTokens(NewReader(strings.NewReader(doc), Options{BufferSize: 4, MaxBufferBytes: 1 << 16, Lenient: true, Entities: htmlentity.Lookup, AutoClose: htmlentity.AutoClose}))} {
			if len(got) != len(want) {
				t.Errorf("%q:\n got %+v\nwant %+v", doc, got, want)
				continue
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("%q: token %d got %+v want %+v", doc, i, got[i], want[i])
				}
			}
		}
	}
	// Without Lenient the mismatch is still refused.
	if _, err := tokens(NewBytesReader([]byte(`<a><b></a>`), Options{})); err == nil {
		t.Error("strict reader accepted a mismatched end tag")
	}
}

func TestEntityTableDecodesWhatTheFiveDoNot(t *testing.T) {
	doc := `<a x="&nbsp;&amp;&lt;"><b>&copy; &amp; &#65; &nope;</b><c>&amp2;</c></a>`
	ent := func(name []byte) (string, bool) {
		if Equal(name, "amp2") {
			return "&", true
		}
		return htmlentity.Lookup(name)
	}
	sourcesWith(t, doc, Options{Entities: ent}, func(t *testing.T, r *Reader) {
		r.Next()
		if v, _ := r.Attr("x"); v.String() != "\u00a0&<" {
			t.Errorf("attribute: %q", v.String())
		}
		r.Next()
		if v, err := r.ElementText(); err != nil || v.String() != "© & A &nope;" {
			t.Errorf("b: %q %v", v, err)
		}
		r.Next()
		if v, err := r.ElementText(); err != nil || v.String() != "&" {
			t.Errorf("c: %q %v", v, err)
		}
	})
	if _, err := tokens(NewBytesReader([]byte(doc), Options{})); err != nil {
		t.Errorf("without a table: %v", err)
	}
}

func TestDoctypeInternalSubsetDeclaresEntities(t *testing.T) {
	doc := `<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd" [
	<!ENTITY ns_svg "http://www.w3.org/2000/svg">
	<!-- a ] in a comment <!ENTITY no "x"> -->
	<!ENTITY % pe "ignored"> <!ENTITY ext SYSTEM "x.ent"> <?pi ] ?>
	<!ENTITY amp2 "&amp; &#65;"> <!ENTITY q "]&gt;">
]>
<svg xmlns="&ns_svg;" a="&q;">&amp2;&ext;</svg>`
	sourcesWith(t, doc, Options{AllowDoctype: true}, func(t *testing.T, r *Reader) {
		got, err := tokens(r)
		if err != nil || got[0].kind != Directive || got[len(got)-1].kind != EOF {
			t.Fatalf("got %+v, %v", got, err)
		}
		r.ResetBytes([]byte(doc))
		r.Next()
		r.Next()
		r.Next()
		if ns := r.Namespace(); ns != "http://www.w3.org/2000/svg" {
			t.Errorf("namespace from a declared entity: %q", ns)
		}
		if v, _ := r.Attr("a"); v.String() != "]>" {
			t.Errorf("a: %q", v.String())
		}
		if v, err := r.ElementText(); err != nil || v.String() != "& A&ext;" {
			t.Errorf("text: %q %v", v, err)
		}
	})
	if _, err := tokens(NewBytesReader([]byte(doc), Options{})); !errors.Is(err, ErrDoctype) {
		t.Errorf("without AllowDoctype: %v", err)
	}
}

func TestCharsetReaderConvertsTheRest(t *testing.T) {
	doc := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><a x=\"\xe9\">caf\xe9</a>"
	latin1 := func(label string, src io.Reader) (io.Reader, error) {
		if label != "ISO-8859-1" {
			t.Errorf("label %q", label)
		}
		b, _ := io.ReadAll(src)
		var out []byte
		for _, c := range b {
			out = append(out, string(rune(c))...)
		}
		return strings.NewReader(string(out)), nil
	}
	sourcesWith(t, doc, Options{CharsetReader: latin1}, func(t *testing.T, r *Reader) {
		if k, err := r.Next(); err != nil || k != ProcInst || !r.NameIs("xml") {
			t.Fatalf("declaration: %v %v", k, err)
		}
		r.Next()
		if v, _ := r.Attr("x"); v.String() != "é" {
			t.Errorf("attribute: %q", v.String())
		}
		if v, err := r.ElementText(); err != nil || v.String() != "café" {
			t.Errorf("text: %q %v", v, err)
		}
	})
	if _, err := tokens(NewBytesReader([]byte(doc), Options{})); !errors.Is(err, ErrEncoding) {
		t.Errorf("without a hook: %v", err)
	}
	boom := errors.New("boom")
	r := NewBytesReader([]byte(doc), Options{CharsetReader: func(string, io.Reader) (io.Reader, error) { return nil, boom }})
	var ce *CharsetError
	if _, err := tokens(r); !errors.As(err, &ce) || !errors.Is(err, boom) {
		t.Errorf("hook error: %v", err)
	}
}

func TestHTMLEntityTableMatchesEncodingXML(t *testing.T) {
	for name, want := range xml.HTMLEntity {
		if got, ok := htmlentity.Lookup([]byte(name)); !ok || got != want {
			t.Errorf("%s: %q %v, want %q", name, got, ok, want)
		}
	}
	if _, ok := htmlentity.Lookup([]byte("nope")); ok {
		t.Error("nope resolved")
	}
	for _, n := range xml.HTMLAutoClose {
		if !htmlentity.AutoClose([]byte(strings.ToUpper(n))) || !htmlentity.AutoClose([]byte("xhtml:"+n)) {
			t.Errorf("%s not auto-closed", n)
		}
	}
	if htmlentity.AutoClose([]byte("div")) {
		t.Error("div auto-closed")
	}
}
