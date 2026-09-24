package xml

import (
	"bytes"
	stdxml "encoding/xml"
	"strconv"
	"testing"
)

// genSheet builds a worksheet part the way a spreadsheet writer does: one
// element shape repeated tens of thousands of times, attributes carrying most
// of the information, no whitespace.
func genSheet(rows, cols int) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\r\n")
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" mc:Ignorable="x14ac" xmlns:x14ac="http://schemas.microsoft.com/office/spreadsheetml/2009/9/ac">`)
	b.WriteString(`<dimension ref="A1:T` + strconv.Itoa(rows) + `"/><sheetViews><sheetView tabSelected="1" workbookViewId="0"/></sheetViews><sheetFormatPr defaultRowHeight="15" x14ac:dyDescent="0.25"/>`)
	b.WriteString(`<cols><col min="1" max="1" width="12.5" customWidth="1"/></cols><sheetData>`)
	for r := 1; r <= rows; r++ {
		b.WriteString(`<row r="` + strconv.Itoa(r) + `" spans="1:` + strconv.Itoa(cols) + `" x14ac:dyDescent="0.25">`)
		for c := 0; c < cols; c++ {
			ref := string(rune('A'+c)) + strconv.Itoa(r)
			switch c % 4 {
			case 0:
				b.WriteString(`<c r="` + ref + `" s="1" t="s"><v>` + strconv.Itoa((r*cols+c)%500) + `</v></c>`)
			case 1:
				b.WriteString(`<c r="` + ref + `"><v>` + strconv.Itoa(r*c) + `.25</v></c>`)
			case 2:
				b.WriteString(`<c r="` + ref + `" s="2"><f>A` + strconv.Itoa(r) + `*2</f><v>` + strconv.Itoa(r*2) + `</v></c>`)
			case 3:
				b.WriteString(`<c r="` + ref + `"><v>` + strconv.Itoa(c) + `</v></c>`)
			}
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData><pageMargins left="0.7" right="0.7" top="0.75" bottom="0.75" header="0.3" footer="0.3"/></worksheet>`)
	return b.Bytes()
}

// genSharedStrings builds a sharedStrings part: text heavy, with an entity
// in some strings and a rich-text run in some.
func genSharedStrings(n int) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\r\n")
	b.WriteString(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="` + strconv.Itoa(n) + `" uniqueCount="` + strconv.Itoa(n) + `">`)
	for i := range n {
		switch i % 5 {
		case 0:
			b.WriteString(`<si><t>Customer ` + strconv.Itoa(i) + ` &amp; Sons</t></si>`)
		case 1:
			b.WriteString(`<si><t xml:space="preserve"> padded ` + strconv.Itoa(i) + ` </t></si>`)
		case 2:
			b.WriteString(`<si><r><rPr><b/></rPr><t>bold</t></r><r><t> plain ` + strconv.Itoa(i) + `</t></r></si>`)
		default:
			b.WriteString(`<si><t>Item number ` + strconv.Itoa(i) + ` with a description long enough to matter</t></si>`)
		}
	}
	b.WriteString(`</sst>`)
	return b.Bytes()
}

var (
	benchSheet = genSheet(2000, 20)
	benchSst   = genSharedStrings(20000)
)

// The token loop alone, with nothing kept: how fast each reader moves.

func BenchmarkSheetScan_StdRawToken(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	for b.Loop() {
		d := stdxml.NewDecoder(bytes.NewReader(benchSheet))
		for {
			if _, err := d.RawToken(); err != nil {
				break
			}
		}
	}
}

func BenchmarkSheetScan_StdToken(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	for b.Loop() {
		d := stdxml.NewDecoder(bytes.NewReader(benchSheet))
		for {
			if _, err := d.Token(); err != nil {
				break
			}
		}
	}
}

func BenchmarkSheetScan_Reader(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSheet)
	r := NewReader(src, Options{})
	for b.Loop() {
		src.Reset(benchSheet)
		r.Reset(src)
		for {
			k, err := r.Next()
			if err != nil {
				b.Fatal(err)
			}
			if k == EOF {
				break
			}
		}
	}
}

func BenchmarkSheetScan_BytesReader(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	r := NewBytesReader(benchSheet, Options{})
	for b.Loop() {
		r.ResetBytes(benchSheet)
		for {
			k, err := r.Next()
			if err != nil {
				b.Fatal(err)
			}
			if k == EOF {
				break
			}
		}
	}
}

// Skipping the sheet body: what a caller pays to reach the elements after it.
func BenchmarkSheetSkipBody_Reader(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSheet)
	r := NewReader(src, Options{})
	for b.Loop() {
		src.Reset(benchSheet)
		r.Reset(src)
		for {
			k, err := r.Next()
			if err != nil {
				b.Fatal(err)
			}
			if k == EOF {
				break
			}
			if k == StartElement && r.NameIs("sheetData") {
				if err := r.Skip(); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}

// Decoding into structs: the same target shape, filled by encoding/xml's
// reflection and by a hand-written Decodable.

type stdWorksheet struct {
	XMLName stdxml.Name `xml:"worksheet"`
	Rows    []stdRow    `xml:"sheetData>row"`
}

type stdRow struct {
	R     int       `xml:"r,attr"`
	Cells []stdCell `xml:"c"`
}

type stdCell struct {
	R string `xml:"r,attr"`
	S int    `xml:"s,attr"`
	T string `xml:"t,attr"`
	V string `xml:"v"`
}

func BenchmarkSheetDecode_StdUnmarshal(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	for b.Loop() {
		var ws stdWorksheet
		if err := stdxml.Unmarshal(benchSheet, &ws); err != nil {
			b.Fatal(err)
		}
		if len(ws.Rows) != 2000 {
			b.Fatal("rows")
		}
	}
}

// worksheet is the same shape read through Decodable, keeping the strings
// as strings, so the allocations left are the ones the caller asked for.
type worksheet struct {
	Rows []row
}

type row struct {
	R     int64
	Cells []stdCell
}

func (w *worksheet) DecodeXMLFrom(r *Reader) error {
	w.Rows = w.Rows[:0]
	ws := r.Element()
	for {
		ok, err := r.NextChild(ws)
		if err != nil || !ok {
			return err
		}
		if !r.NameIs("sheetData") {
			continue
		}
		sd := r.Element()
		for {
			ok, err := r.NextChild(sd)
			if err != nil || !ok {
				break
			}
			if len(w.Rows) == cap(w.Rows) {
				w.Rows = append(w.Rows, row{})
			} else {
				w.Rows = w.Rows[:len(w.Rows)+1]
			}
			if err := r.Decode(&w.Rows[len(w.Rows)-1]); err != nil {
				return err
			}
		}
		if err != nil {
			return err
		}
	}
}

func (rw *row) DecodeXMLFrom(r *Reader) error {
	rw.Cells = rw.Cells[:0]
	if v, ok := r.Attr("r"); ok {
		n, err := v.Int()
		if err != nil {
			return err
		}
		rw.R = n
	}
	el := r.Element()
	for {
		ok, err := r.NextChild(el)
		if err != nil || !ok {
			return err
		}
		if !r.NameIs("c") {
			continue
		}
		var c stdCell
		if v, ok := r.Attr("r"); ok {
			c.R = v.String()
		}
		if v, ok := r.Attr("s"); ok {
			n, err := v.Int()
			if err != nil {
				return err
			}
			c.S = int(n)
		}
		if v, ok := r.Attr("t"); ok {
			c.T = v.String()
		}
		cell := r.Element()
		for {
			ok, err := r.NextChild(cell)
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			if r.NameIs("v") {
				v, err := r.ElementText()
				if err != nil {
					return err
				}
				c.V = v.String()
			}
		}
		rw.Cells = append(rw.Cells, c)
	}
}

func BenchmarkSheetDecode_Reader(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSheet)
	r := NewReader(src, Options{})
	var ws worksheet
	for b.Loop() {
		src.Reset(benchSheet)
		r.Reset(src)
		for {
			k, err := r.Next()
			if err != nil {
				b.Fatal(err)
			}
			if k == StartElement {
				break
			}
		}
		if err := r.Decode(&ws); err != nil {
			b.Fatal(err)
		}
		if len(ws.Rows) != 2000 {
			b.Fatal("rows")
		}
	}
}

// typedCell is what a spreadsheet reader actually wants: the reference
// parsed, the number parsed, the shared string as an index. Nothing here is
// a string, so nothing allocates.
type typedCell struct {
	Col, Row int
	Style    int
	Shared   bool
	Num      float64
	SharedID int
}

type typedSheet struct {
	Cells []typedCell
}

func parseRef(ref Value) (col, row int) {
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		col = col*26 + int(ref[i]-'A'+1)
		i++
	}
	for i < len(ref) && ref[i] >= '0' && ref[i] <= '9' {
		row = row*10 + int(ref[i]-'0')
		i++
	}
	return col, row
}

func (s *typedSheet) DecodeXMLFrom(r *Reader) error {
	s.Cells = s.Cells[:0]
	ws := r.Element()
	for {
		ok, err := r.NextChild(ws)
		if err != nil || !ok {
			return err
		}
		if !r.NameIs("sheetData") {
			continue
		}
		sd := r.Element()
		for {
			ok, err := r.NextChild(sd)
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			rw := r.Element()
			for {
				ok, err := r.NextChild(rw)
				if err != nil {
					return err
				}
				if !ok {
					break
				}
				var c typedCell
				ref, _ := r.Attr("r")
				c.Col, c.Row = parseRef(ref)
				if v, ok := r.Attr("s"); ok {
					n, err := v.Int()
					if err != nil {
						return err
					}
					c.Style = int(n)
				}
				if v, ok := r.Attr("t"); ok {
					c.Shared = v.Equal("s")
				}
				cell := r.Element()
				for {
					ok, err := r.NextChild(cell)
					if err != nil {
						return err
					}
					if !ok {
						break
					}
					if !r.NameIs("v") {
						continue
					}
					v, err := r.ElementText()
					if err != nil {
						return err
					}
					if c.Shared {
						n, err := v.Int()
						if err != nil {
							return err
						}
						c.SharedID = int(n)
					} else if c.Num, err = v.Float(); err != nil {
						return err
					}
				}
				s.Cells = append(s.Cells, c)
			}
		}
	}
}

func BenchmarkSheetDecode_ReaderTyped(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSheet)
	r := NewReader(src, Options{})
	var s typedSheet
	for b.Loop() {
		src.Reset(benchSheet)
		r.Reset(src)
		for {
			k, err := r.Next()
			if err != nil {
				b.Fatal(err)
			}
			if k == StartElement {
				break
			}
		}
		if err := r.Decode(&s); err != nil {
			b.Fatal(err)
		}
		if len(s.Cells) != 40000 {
			b.Fatal("cells")
		}
	}
}

// Shared strings: text heavy, with entities and rich-text runs.

type stdSst struct {
	XMLName stdxml.Name `xml:"sst"`
	SI      []struct {
		T string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

func BenchmarkSstDecode_StdUnmarshal(b *testing.B) {
	b.SetBytes(int64(len(benchSst)))
	b.ReportAllocs()
	for b.Loop() {
		var sst stdSst
		if err := stdxml.Unmarshal(benchSst, &sst); err != nil {
			b.Fatal(err)
		}
		if len(sst.SI) != 20000 {
			b.Fatal("count")
		}
	}
}

// One string per <si>, rich-text runs concatenated, one allocation each,
// which is the one the caller asked for.
func BenchmarkSstDecode_Reader(b *testing.B) {
	b.SetBytes(int64(len(benchSst)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSst)
	r := NewReader(src, Options{})
	strs := make([]string, 0, 20000)
	var scratch []byte
	for b.Loop() {
		src.Reset(benchSst)
		r.Reset(src)
		strs = strs[:0]
		for {
			k, err := r.Next()
			if err != nil {
				b.Fatal(err)
			}
			if k == StartElement {
				break
			}
		}
		sst := r.Element()
		for {
			ok, err := r.NextChild(sst)
			if err != nil {
				b.Fatal(err)
			}
			if !ok {
				break
			}
			si := r.Element()
			scratch = scratch[:0]
			for {
				ok, err := r.NextChild(si)
				if err != nil {
					b.Fatal(err)
				}
				if !ok {
					break
				}
				// An if chain rather than a tagless switch: go1.27.0's
				// escape analysis crashes on a tagless switch inside a
				// b.Loop body.
				if r.NameIs("t") {
					v, err := r.ElementText()
					if err != nil {
						b.Fatal(err)
					}
					scratch = append(scratch, v...)
				} else if r.NameIs("r") {
					run := r.Element()
					for {
						ok, err := r.NextChild(run)
						if err != nil {
							b.Fatal(err)
						}
						if !ok {
							break
						}
						if r.NameIs("t") {
							v, err := r.ElementText()
							if err != nil {
								b.Fatal(err)
							}
							scratch = append(scratch, v...)
						}
					}
				}
			}
			strs = append(strs, string(scratch))
		}
		if len(strs) != 20000 {
			b.Fatal("count")
		}
	}
}
