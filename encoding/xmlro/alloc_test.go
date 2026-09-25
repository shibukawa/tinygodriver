package xmlro

import (
	"bytes"
	"testing"
)

// Every operation of the explicit surface allocates nothing in steady state,
// on both compilers. The iterator forms are the exception under TinyGo,
// where each range loop allocates its closure contexts; iterBudget, set per
// compiler, pins that number so it cannot grow unnoticed.
func TestEachOperationAllocatesNothing(t *testing.T) {
	doc := []byte(`<row r="1"><c r="A1" s="1" t="s"><v>0</v></c><c r="B1"><v>12.5</v></c><c r="C1" t="s" x="a&amp;b"><v>3</v></c></row>`)
	src := bytes.NewReader(doc)
	r := NewReader(src, Options{})
	var c cell
	var isum int64
	var fsum float64
	cases := []struct {
		name   string
		budget uint64 // bytes per run
		fn     func()
	}{
		{"stream reset and token loop", 0, func() {
			src.Reset(doc)
			r.Reset(src)
			for k, _ := r.Next(); k != EOF && k != None; k, _ = r.Next() {
			}
		}},
		{"byte slice reset and token loop", 0, func() {
			r.ResetBytes(doc)
			for k, _ := r.Next(); k != EOF && k != None; k, _ = r.Next() {
			}
		}},
		{"NameIs, Attr, Value.Int, Value.Equal", 0, func() {
			r.ResetBytes(doc)
			for k, _ := r.Next(); k != EOF && k != None; k, _ = r.Next() {
				if k == StartElement && r.NameIs("c") {
					if v, ok := r.Attr("s"); ok {
						n, _ := v.Int()
						isum += n
					}
					if v, ok := r.Attr("t"); ok && v.Equal("s") {
						isum++
					}
				}
			}
		}},
		{"ElementText and Value.Float", 0, func() {
			r.ResetBytes(doc)
			for k, _ := r.Next(); k != EOF && k != None; k, _ = r.Next() {
				if k == StartElement && r.NameIs("v") {
					v, _ := r.ElementText()
					f, _ := v.Float()
					fsum += f
				}
			}
		}},
		{"Element and NextChild", 0, func() {
			r.ResetBytes(doc)
			r.Next()
			row := r.Element()
			for ok, _ := r.NextChild(row); ok; ok, _ = r.NextChild(row) {
			}
		}},
		{"Skip", 0, func() {
			r.ResetBytes(doc)
			r.Next()
			r.Skip()
		}},
		{"RawElement", 0, func() {
			r.ResetBytes(doc)
			r.Next()
			raw, _ := r.RawElement()
			isum += int64(len(raw))
		}},
		{"Decode through the interface", 0, func() {
			r.ResetBytes(doc)
			r.Next()
			row := r.Element()
			for ok, _ := r.NextChild(row); ok; ok, _ = r.NextChild(row) {
				r.Decode(&c)
			}
		}},
		{"Namespace and LookupNamespace", 0, func() {
			r.ResetBytes(doc)
			r.Next()
			isum += int64(len(r.Namespace()))
			if _, ok := r.LookupNamespace([]byte("xml")); ok {
				isum++
			}
		}},
		{"Tokens loop", iterBudget.tokens, func() {
			r.ResetBytes(doc)
			for range r.Tokens() {
			}
		}},
		{"Children loop", iterBudget.children, func() {
			r.ResetBytes(doc)
			r.Next()
			for range r.Children(r.Element()) {
			}
		}},
	}
	for _, tc := range cases {
		tc.fn() // warm: size the buffers and any table that grows once
		got := allocatedBytes(100, tc.fn) / 100
		if got > tc.budget {
			t.Errorf("%s: %d bytes per run, budget %d", tc.name, got, tc.budget)
		}
	}
	_, _ = isum, fsum
}
