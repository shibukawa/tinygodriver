package xml

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

type tok struct {
	kind Kind
	name string
	text string
}

// tokens drains r and returns what it saw, stopping at the first error.
func tokens(r *Reader) ([]tok, error) {
	var out []tok
	for {
		k, err := r.Next()
		if err != nil {
			return out, err
		}
		t := tok{kind: k}
		switch k {
		case StartElement, EndElement, ProcInst:
			t.name = string(r.Name())
		}
		switch k {
		case Text, CData, Comment, ProcInst, Directive:
			t.text = string(r.Text())
		}
		out = append(out, t)
		if k == EOF {
			return out, nil
		}
	}
}

// sources runs the same document through every input shape the reader
// supports: a byte slice, a stream through a buffer of the default size, a
// stream through a buffer too small for most tokens, and a stream that
// delivers one byte per Read.
func sources(t *testing.T, doc string, fn func(t *testing.T, r *Reader)) {
	t.Helper()
	t.Run("bytes", func(t *testing.T) { fn(t, NewBytesReader([]byte(doc), Options{})) })
	t.Run("stream", func(t *testing.T) { fn(t, NewReader(strings.NewReader(doc), Options{})) })
	t.Run("tiny", func(t *testing.T) {
		fn(t, NewReader(strings.NewReader(doc), Options{BufferSize: 4, MaxBufferBytes: 1 << 16}))
	})
	t.Run("onebyte", func(t *testing.T) {
		fn(t, NewReader(iotest.OneByteReader(strings.NewReader(doc)), Options{BufferSize: 8, MaxBufferBytes: 1 << 16}))
	})
}

func TestTokensOfASmallDocument(t *testing.T) {
	const doc = "\xEF\xBB\xBF<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<w:p a='1' b=\"x>y\"><!-- c --><w:r/><w:t xml:space=\"preserve\"> a &amp; b </w:t><![CDATA[<raw>]]></w:p>\n"
	want := []tok{
		{kind: ProcInst, name: "xml", text: ` version="1.0" encoding="UTF-8"`},
		{kind: Text, text: "\n"},
		{kind: StartElement, name: "w:p"},
		{kind: Comment, text: " c "},
		{kind: StartElement, name: "w:r"},
		{kind: EndElement, name: "w:r"},
		{kind: StartElement, name: "w:t"},
		{kind: Text, text: " a &amp; b "},
		{kind: EndElement, name: "w:t"},
		{kind: CData, text: "<raw>"},
		{kind: EndElement, name: "w:p"},
		{kind: Text, text: "\n"},
		{kind: EOF},
	}
	sources(t, doc, func(t *testing.T, r *Reader) {
		got, err := tokens(r)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
			return
		}
		if len(got) != len(want) {
			t.Fatalf("got %d tokens, want %d: %+v", len(got), len(want), got)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("token %d: got %+v, want %+v", i, got[i], want[i])
			}
		}
	})
}

func TestAttributesAreFoundByNameAndInOrder(t *testing.T) {
	const doc = `<c r="A1" s='2'  t = "s" empty="" ns:x="1&lt;2"/>`
	sources(t, doc, func(t *testing.T, r *Reader) {
		if k, err := r.Next(); err != nil || k != StartElement {
			t.Fatalf("got %v, %v", k, err)
			return
		}
		for _, c := range []struct{ name, want string }{{"r", "A1"}, {"s", "2"}, {"t", "s"}, {"empty", ""}, {"ns:x", "1<2"}} {
			v, ok := r.Attr(c.name)
			if !ok {
				t.Errorf("attribute %q not found", c.name)
				continue
			}
			if got := v.String(); got != c.want {
				t.Errorf("attribute %q: got %q, want %q", c.name, got, c.want)
			}
		}
		if _, ok := r.Attr("missing"); ok {
			t.Error("found an attribute that is not there")
		}
		var names []string
		for {
			n, _, ok := r.NextAttr()
			if !ok {
				break
			}
			names = append(names, string(n))
		}
		if got := strings.Join(names, ","); got != "r,s,t,empty,ns:x" {
			t.Errorf("attribute order: got %q", got)
		}
		if k, err := r.Next(); err != nil || k != EndElement {
			t.Fatalf("after a self-closing tag got %v, %v", k, err)
			return
		}
	})
}

func TestNamePartsAndComparison(t *testing.T) {
	r := NewBytesReader([]byte(`<w:p/>`), Options{})
	if _, err := r.Next(); err != nil {
		t.Fatal(err)
		return
	}
	if !r.NameIs("w:p") || r.NameIs("w:r") {
		t.Error("NameIs compares the qualified name")
	}
	if string(r.LocalName()) != "p" || string(r.Prefix()) != "w" {
		t.Errorf("local %q prefix %q", r.LocalName(), r.Prefix())
	}
	r.ResetBytes([]byte(`<p/>`))
	r.Next()
	if string(r.LocalName()) != "p" || r.Prefix() != nil {
		t.Errorf("unprefixed: local %q prefix %q", r.LocalName(), r.Prefix())
	}
}

func TestNextChildSkipsWhatTheCallerLeaves(t *testing.T) {
	const doc = `<row r="1"> <c r="A1"><v>1</v></c><c r="B1"><is><t>x</t></is></c><c r="C1"/><c r="D1"><v>4</v></c></row>`
	sources(t, doc, func(t *testing.T, r *Reader) {
		if _, err := r.Next(); err != nil {
			t.Fatal(err)
			return
		}
		row := r.Element()
		var seen []string
		for i := 0; ; i++ {
			ok, err := r.NextChild(row)
			if err != nil {
				t.Fatal(err)
				return
			}
			if !ok {
				break
			}
			ref, _ := r.Attr("r")
			seen = append(seen, ref.String())
			switch i {
			case 0:
				// Consume fully.
				cell := r.Element()
				for {
					ok, err := r.NextChild(cell)
					if err != nil || !ok {
						break
					}
					if _, err := r.ElementText(); err != nil {
						t.Fatal(err)
						return
					}
				}
			case 1:
				// Descend one level and stop there, leaving <t> unread.
				cell := r.Element()
				if ok, _ := r.NextChild(cell); !ok || !r.NameIs("is") {
					t.Errorf("expected <is>, got %q", r.Name())
				}
			case 2, 3:
				// Leave untouched.
			}
		}
		if got := strings.Join(seen, ","); got != "A1,B1,C1,D1" {
			t.Errorf("children: got %q", got)
		}
		if r.Kind() != EndElement || !r.NameIs("row") || r.Depth() != 0 {
			t.Errorf("loop ended on %v %q depth %d", r.Kind(), r.Name(), r.Depth())
		}
		if k, err := r.Next(); k != EOF || err != nil {
			t.Errorf("after the row: %v %v", k, err)
		}
	})
}

func TestElementTextJoinsRunsAndDecodesEntities(t *testing.T) {
	cases := []struct{ doc, want string }{
		{`<t>plain</t>`, "plain"},
		{`<t></t>`, ""},
		{`<t/>`, ""},
		{`<t>a &amp; b</t>`, "a & b"},
		{`<t>a<!-- x -->b</t>`, "ab"},
		{`<t>a<![CDATA[&amp;]]>b</t>`, "a&amp;b"},
		{`<t><![CDATA[&lt;]]></t>`, "&lt;"},
		{`<t>a<b>hidden</b>c &lt; d</t>`, "ac < d"},
		{`<t>&#65;&#x42;&#x1F600;&unknown;&#xD800;</t>`, "AB😀&unknown;&#xD800;"},
	}
	for _, c := range cases {
		sources(t, c.doc, func(t *testing.T, r *Reader) {
			if _, err := r.Next(); err != nil {
				t.Fatal(err)
				return
			}
			v, err := r.ElementText()
			if err != nil {
				t.Fatal(err)
				return
			}
			if got := string(v); got != c.want {
				t.Errorf("%s: got %q, want %q", c.doc, got, c.want)
			}
			if r.Kind() != EndElement {
				t.Errorf("%s: ended on %v", c.doc, r.Kind())
			}
		})
	}
}

func TestATextRunLongerThanTheBufferIsHeldWhole(t *testing.T) {
	long := strings.Repeat("0123456789", 1000)
	doc := "<t>" + long + "</t>"
	r := NewReader(strings.NewReader(doc), Options{BufferSize: 16, MaxBufferBytes: 1 << 20})
	r.Next()
	v, err := r.ElementText()
	if err != nil {
		t.Fatal(err)
		return
	}
	if string(v) != long {
		t.Errorf("got %d bytes, want %d", len(v), len(long))
	}
	r = NewReader(strings.NewReader(doc), Options{BufferSize: 16, MaxBufferBytes: 1024})
	r.Next()
	if _, err := r.ElementText(); !errors.Is(err, ErrTooLarge) {
		t.Errorf("over the bound: got %v, want ErrTooLarge", err)
	}
}

func TestRawElementCapturesTheSubtree(t *testing.T) {
	const doc = `<a><b x="1"><c/>text</b><d/></a>`
	sources(t, doc, func(t *testing.T, r *Reader) {
		r.Next()
		a := r.Element()
		r.NextChild(a)
		raw, err := r.RawElement()
		if err != nil {
			t.Fatal(err)
			return
		}
		if string(raw) != `<b x="1"><c/>text</b>` {
			t.Errorf("got %q", raw)
		}
		if ok, _ := r.NextChild(a); !ok || !r.NameIs("d") {
			t.Errorf("after capture expected <d>, got %q", r.Name())
		}
		raw, err = r.RawElement()
		if err != nil || string(raw) != "<d/>" {
			t.Errorf("self-closing capture: %q %v", raw, err)
		}
	})
}

func TestMalformedInputIsRefused(t *testing.T) {
	cases := []struct {
		doc  string
		want error
		msg  string
	}{
		{"<a>", ErrTruncated, ""},
		{"<a", ErrTruncated, ""},
		{"<a></b>", nil, "does not match"},
		{"</a>", nil, "no open element"},
		{"<a></a", ErrTruncated, ""},
		{"<a x=1/>", nil, ""}, // detected lazily, see below
		{"<a></a >", nil, ""},
		{"<a/ >", nil, "unexpected '/'"},
		{"<a><b</a>", nil, "unexpected '<'"},
		{"<!DOCTYPE a><a/>", ErrDoctype, ""},
		{"<!-- x", ErrTruncated, ""},
		{"<![CDATA[ x", ErrTruncated, ""},
		{"<!bogus>", nil, "unexpected '<!'"},
		{`<?xml version="1.0" encoding="Shift_JIS"?><a/>`, ErrEncoding, ""},
		{`<?xml version="1.0" encoding="utf-8"?><a/>`, nil, ""},
	}
	for _, c := range cases {
		sources(t, c.doc, func(t *testing.T, r *Reader) {
			_, err := tokens(r)
			if c.want == nil && c.msg == "" {
				if err != nil {
					t.Errorf("%q: unexpected error %v", c.doc, err)
				}
				return
			}
			if err == nil {
				t.Errorf("%q: accepted", c.doc)
				return
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("%q: got %v, want %v", c.doc, err, c.want)
			}
			var se *SyntaxError
			if c.msg != "" && (!errors.As(err, &se) || !strings.Contains(se.Msg, c.msg)) {
				t.Errorf("%q: got %v, want a syntax error containing %q", c.doc, err, c.msg)
			}
		})
	}
}

func TestAMalformedAttributeSurfacesOnTheNextAdvance(t *testing.T) {
	r := NewBytesReader([]byte(`<a x=1></a>`), Options{})
	r.Next()
	if _, ok := r.Attr("x"); ok {
		t.Error("unquoted value accepted")
	}
	if _, err := r.Next(); err == nil {
		t.Error("the malformed attribute was not reported")
	}
}

func TestDoctypeIsReportedWhenAllowed(t *testing.T) {
	r := NewBytesReader([]byte(`<!DOCTYPE html><a/>`), Options{AllowDoctype: true})
	got, err := tokens(r)
	if err != nil || got[0].kind != Directive || got[0].text != " html" {
		t.Errorf("got %+v, %v", got, err)
	}
	r = NewBytesReader([]byte(`<!DOCTYPE a [<!ENTITY x "y">]><a/>`), Options{AllowDoctype: true})
	if _, err := tokens(r); err == nil {
		t.Error("an internal subset was accepted")
	}
}

func TestDepthIsBounded(t *testing.T) {
	doc := strings.Repeat("<a>", 20) + strings.Repeat("</a>", 20)
	r := NewBytesReader([]byte(doc), Options{MaxDepth: 20})
	if _, err := tokens(r); err != nil {
		t.Errorf("at the bound: %v", err)
	}
	r = NewBytesReader([]byte(doc), Options{MaxDepth: 19})
	if _, err := tokens(r); !errors.Is(err, ErrTooDeep) {
		t.Errorf("past the bound: got %v", err)
	}
}

func TestErrorsAreSticky(t *testing.T) {
	r := NewBytesReader([]byte(`</a>`), Options{})
	_, err1 := r.Next()
	_, err2 := r.Next()
	if err1 == nil || err2 != err1 {
		t.Errorf("got %v then %v", err1, err2)
	}
}

func TestSourceErrorsPropagate(t *testing.T) {
	boom := errors.New("boom")
	r := NewReader(iotest.ErrReader(boom), Options{})
	if _, err := r.Next(); !errors.Is(err, boom) {
		t.Errorf("got %v", err)
	}
	r = NewReader(io.MultiReader(strings.NewReader("<a>"), iotest.ErrReader(boom)), Options{})
	if _, err := tokens(r); !errors.Is(err, boom) {
		t.Errorf("mid-document: got %v", err)
	}
}

func TestOffsetCountsDocumentBytes(t *testing.T) {
	doc := "<a>" + strings.Repeat("x", 100) + "</a>"
	r := NewReader(strings.NewReader(doc), Options{BufferSize: 8, MaxBufferBytes: 1 << 16})
	r.Next()
	r.Next()
	if got := r.Offset(); got != 103 {
		t.Errorf("after the text: offset %d, want 103", got)
	}
}

func TestValueParsing(t *testing.T) {
	if n, err := Value("42").Int(); n != 42 || err != nil {
		t.Errorf("Int: %d %v", n, err)
	}
	if _, err := Value("4x").Int(); err == nil {
		t.Error("Int accepted junk")
	}
	if f, err := Value("2.5").Float(); f != 2.5 || err != nil {
		t.Errorf("Float: %v %v", f, err)
	}
	if u, err := Value("7").Uint(); u != 7 || err != nil {
		t.Errorf("Uint: %d %v", u, err)
	}
	for _, c := range []struct {
		in   string
		want bool
		ok   bool
	}{{"1", true, true}, {"0", false, true}, {"true", true, true}, {"false", false, true}, {"yes", false, false}} {
		got, err := Value(c.in).Bool()
		if got != c.want || (err == nil) != c.ok {
			t.Errorf("Bool(%q): %v %v", c.in, got, err)
		}
	}
	if !Value("a &amp; b").Equal("a & b") || Value("a &amp; b").Equal("a &amp; b") {
		t.Error("Equal compares decoded content")
	}
	if !Value("UTF-8").EqualFold("utf-8") || Value("UTF-8").EqualFold("utf-16") {
		t.Error("EqualFold")
	}
	var buf []byte
	buf = Value("x&lt;y").AppendTo(buf)
	buf = Value("z").AppendTo(buf)
	if string(buf) != "x<yz" {
		t.Errorf("AppendTo: %q", buf)
	}
}

func TestUnescapeEdges(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"&", "&"},
		{"&;", "&;"},
		{"&amp", "&amp"},
		{"a&amp;&lt;&gt;&apos;&quot;b", "a&<>'\"b"},
		{"&#;", "&#;"},
		{"&#x;", "&#x;"},
		{"&#99999999999;", "&#99999999999;"},
		{"&averyveryverylongname;", "&averyveryverylongname;"},
	} {
		if got := string(Unescape(nil, []byte(c.in))); got != c.want {
			t.Errorf("Unescape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

type cell struct {
	ref   []byte
	style int64
	value float64
}

func (c *cell) DecodeXMLFrom(r *Reader) error {
	ref, _ := r.Attr("r")
	c.ref = append(c.ref[:0], ref...)
	if s, ok := r.Attr("s"); ok {
		c.style, _ = s.Int()
	}
	el := r.Element()
	for {
		ok, err := r.NextChild(el)
		if err != nil || !ok {
			return err
		}
		if r.NameIs("v") {
			v, err := r.ElementText()
			if err != nil {
				return err
			}
			if c.value, err = v.Float(); err != nil {
				return err
			}
		}
	}
}

func TestDecodeChecksThePostcondition(t *testing.T) {
	r := NewBytesReader([]byte(`<c r="B2" s="3"><f>1+1</f><v>2</v></c>`), Options{})
	r.Next()
	var c cell
	if err := r.Decode(&c); err != nil {
		t.Fatal(err)
		return
	}
	if string(c.ref) != "B2" || c.style != 3 || c.value != 2 {
		t.Errorf("decoded %+v", c)
	}
	bad := decodableFunc(func(r *Reader) error { return nil })
	r.ResetBytes([]byte(`<c/>`))
	r.Next()
	if err := r.Decode(bad); err == nil {
		t.Error("a decoder that consumed nothing was accepted")
	}
}

type decodableFunc func(r *Reader) error

func (f decodableFunc) DecodeXMLFrom(r *Reader) error { return f(r) }

// The point of the package: reading an Office-shaped document through a reused
// Reader allocates nothing once the buffer is sized.
func TestSteadyStateReadingAllocatesNothing(t *testing.T) {
	doc := []byte(`<row r="1"><c r="A1" s="1" t="s"><v>0</v></c><c r="B1"><v>12.5</v></c><c r="C1" t="s" x="a&amp;b"><v>3</v></c></row>`)
	r := NewReader(bytes.NewReader(doc), Options{})
	var c cell
	src := bytes.NewReader(doc)
	run := func() {
		src.Reset(doc)
		r.Reset(src)
		r.Next()
		row := r.Element()
		for {
			ok, err := r.NextChild(row)
			if err != nil {
				t.Fatal(err)
				return
			}
			if !ok {
				break
			}
			if err := r.Decode(&c); err != nil {
				t.Fatal(err)
				return
			}
		}
	}
	run() // size the buffers
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Errorf("steady state allocates %v per document, want 0", allocs)
	}
	rb := NewBytesReader(doc, Options{})
	runBytes := func() {
		rb.ResetBytes(doc)
		rb.Next()
		row := rb.Element()
		for {
			ok, err := rb.NextChild(row)
			if err != nil || !ok {
				break
			}
			if err := rb.Decode(&c); err != nil {
				t.Fatal(err)
				return
			}
		}
	}
	runBytes()
	if allocs := testing.AllocsPerRun(100, runBytes); allocs != 0 {
		t.Errorf("byte-slice steady state allocates %v per document, want 0", allocs)
	}
}

func TestIteratorsMatchTheExplicitLoops(t *testing.T) {
	const doc = `<row r="1"><c r="A1"><v>1</v></c><c r="B1"><is><t>x</t></is></c><c r="C1"/></row>`
	sources(t, doc, func(t *testing.T, r *Reader) {
		var kinds []Kind
		var names []string
		for k := range r.Tokens() {
			kinds = append(kinds, k)
			if k == StartElement && r.NameIs("row") {
				for name := range r.Children(r.Element()) {
					names = append(names, string(name))
					if string(name) == "c" {
						if ref, _ := r.Attr("r"); ref.Equal("B1") {
							// Descend and stop, leaving <t> unread.
							for range r.Children(r.Element()) {
								break
							}
						}
					}
				}
				if r.Kind() != EndElement || !r.NameIs("row") {
					t.Errorf("Children ended on %v %q", r.Kind(), r.Name())
				}
			}
		}
		if err := r.Err(); err != nil {
			t.Fatal(err)
			return
		}
		// Children consumed the row through its end tag, so Tokens saw only its start.
		if strings.Join(names, ",") != "c,c,c" || len(kinds) != 1 {
			t.Errorf("names %v kinds %v", names, kinds)
		}
	})
	r := NewBytesReader([]byte(`<a><b></a>`), Options{})
	n := 0
	for range r.Tokens() {
		n++
	}
	if r.Err() == nil {
		t.Error("the iterator hid the error")
	}
}

func TestIteratorsAllocateNothing(t *testing.T) {
	doc := []byte(`<row r="1"><c r="A1" s="1" t="s"><v>0</v></c><c r="B1"><v>12.5</v></c></row>`)
	src := bytes.NewReader(doc)
	r := NewReader(src, Options{})
	var c cell
	run := func() {
		src.Reset(doc)
		r.Reset(src)
		for k := range r.Tokens() {
			if k != StartElement {
				continue
			}
			for range r.Children(r.Element()) {
				if err := r.Decode(&c); err != nil {
					t.Fatal(err)
					return
				}
			}
		}
	}
	run()
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Errorf("iterator loop allocates %v per document, want 0", allocs)
	}
}
