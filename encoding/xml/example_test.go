package xml_test

import (
	"fmt"
	"strings"

	"github.com/shibukawa/tinygodriver/encoding/xml"
)

// A cell of a worksheet, read by hand: the attributes it wants, the child
// it wants, and nothing kept as a string.
type cell struct {
	Ref    string
	Style  int64
	Shared bool
	Value  float64
	Index  int64
}

func (c *cell) DecodeXMLFrom(r *xml.Reader) error {
	*c = cell{} // the caller reuses one cell, and an absent attribute must not keep the last value
	ref, _ := r.Attr("r")
	c.Ref = ref.String()
	if s, ok := r.Attr("s"); ok {
		c.Style, _ = s.Int()
	}
	if t, ok := r.Attr("t"); ok {
		c.Shared = t.Equal("s")
	}
	for name := range r.Children(r.Element()) {
		if !xml.Equal(name, "v") {
			continue
		}
		v, err := r.ElementText()
		if err != nil {
			return err
		}
		if c.Shared {
			c.Index, err = v.Int()
		} else {
			c.Value, err = v.Float()
		}
		if err != nil {
			return err
		}
	}
	return r.Err()
}

func Example() {
	const sheet = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <dimension ref="A1:B2"/>
  <sheetData>
    <row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" s="2"><v>12.5</v></c></row>
    <row r="2"><c r="A2"><f>B1*2</f><v>25</v></c></row>
  </sheetData>
  <mergeCells count="1"><mergeCell ref="A1:B1"/></mergeCells>
</worksheet>`

	r := xml.NewReader(strings.NewReader(sheet), xml.Options{})
	var c cell
	for k := range r.Tokens() {
		if k != xml.StartElement {
			continue
		}
		switch {
		case r.NameIs("sheetData"):
			for range r.Children(r.Element()) { // each <row>
				for range r.Children(r.Element()) { // each <c>
					if err := r.Decode(&c); err != nil {
						panic(err)
					}
					if c.Shared {
						fmt.Printf("%s style=%d shared string #%d\n", c.Ref, c.Style, c.Index)
					} else {
						fmt.Printf("%s style=%d value=%g\n", c.Ref, c.Style, c.Value)
					}
				}
			}
		case r.NameIs("mergeCell"):
			ref, _ := r.Attr("ref")
			fmt.Printf("merged %s\n", ref)
		}
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	// Output:
	// A1 style=0 shared string #0
	// B1 style=2 value=12.5
	// A2 style=0 value=25
	// merged A1:B1
}
