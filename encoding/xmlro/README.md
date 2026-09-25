# encoding/xmlro

A read-only pull reader for Office Open XML parts: worksheets, shared strings, document
bodies, styles. It reads one token at a time from an `io.Reader` or a byte
slice, hands back names, attributes and text as slices into its own buffer,
and allocates nothing in steady state. A caller copies only what it keeps.

The name says what it is: XML, read only. It never writes, and it does not
share a name with the standard library package it stands beside.

It is a filter, not a mapper. `encoding/xml` in the standard library decodes
any document into any struct; this package reads the handful of elements an
Office consumer wants out of a part that is mostly something else, and skips
the rest at scanning speed.

## Why not the standard library

`encoding/xml` allocates for every token: a `Name` with two strings, an
`[]Attr`, a copied `CharData`. Reading a worksheet with forty thousand cells
through `Decoder.Token` allocates half a million times before the caller keeps
anything, and `Unmarshal` on top of it adds reflection. Measured on the same
worksheet, 1.5 MB, 2000 rows of 20 cells, linux/amd64:

| | ns/op | MB/s | allocs/op |
|---|---|---|---|
| `Decoder.RawToken` loop | 59,363,299 | 25.6 | 364,082 |
| `Decoder.Token` loop | 79,809,134 | 19.0 | 548,110 |
| **`Reader.Next` loop** | **6,953,800** | **218.1** | **0** |
| **`Reader.Skip` over the sheet body** | **5,943,703** | **255.2** | **0** |
| `Unmarshal` into row/cell structs | 131,751,407 | 11.5 | 839,876 |
| **`Decodable` into the same structs** | **12,616,078** | **120.2** | 75,782 (the strings kept) |
| **`Decodable` into typed cells** | **11,471,666** | **132.2** | **0** |
| `Decodable` into typed cells, `Children` loops, one per method | 12,397,310 | 122.4 | 0 |
| `Decodable` into typed cells, four `Children` loops in one method | 20,500,697 | 74.0 | 166,002 |

And on a shared-strings part, 20,000 strings with entities and rich-text runs:

| | MB/s | allocs/op |
|---|---|---|
| `Unmarshal` | 15.0 | 436,058 |
| **`Reader`, one string per `<si>`** | **200.0** | **20,000** |

`go test -bench . ./encoding/xmlro` reproduces the table.

The same benchmarks under TinyGo 0.42.0 on the same machine. TinyGo does
not count objects, so only bytes per operation are meaningful there, and
the standard library's figures are its own encoding/xml compiled by TinyGo:

| | MB/s | B/op |
|---|---|---|
| `Decoder.RawToken` loop | 22.3 | 24,485,984 |
| **`Reader.Next` loop** | **139.7** | **1,017** |
| **`Reader.Skip` over the sheet body** | **294.0** | 632 |
| `Unmarshal` into row/cell structs | 9.1 | 53,796,336 |
| **`Decodable` into typed cells** | **84.2** | 63,939 |
| `Decodable` into typed cells, `Children` loops, one per method | 50.2 | 10,688,355 |
| `Unmarshal` of shared strings | 15.3 | 25,600,384 |
| **`Reader`, one string per `<si>`** | **133.6** | 803,004 |

The bytes on the `Reader` rows are the buffer and the kept strings, sized
once; the iterator row shows what the per-loop closure contexts cost when
the loop is per cell, which is why a TinyGo decoder writes the explicit
loop.

## Reading

```go
r := xmlro.NewReader(part, xmlro.Options{})   // or xmlro.NewBytesReader(data, ...)
for {
	k, err := r.Next()
	if err != nil {
		return err
	}
	if k == xmlro.EOF {
		break
	}
	if k == xmlro.StartElement && r.NameIs("sheetData") {
		err = r.Decode(&sheet)          // sheet implements Decodable
	}
}
```

On a `StartElement` the caller can:

- read `Name`, `LocalName`, `Prefix`, and any attribute with `Attr(name)` or
  all of them with `NextAttr`. The attributes were indexed as the tag was
  scanned, so `Attr` compares against the element's few names and copies
  nothing;
- walk the direct children with `NextChild`, which skips whatever the caller
  does not consume;
- take the element's own text with `ElementText`, entities decoded, children
  skipped;
- capture the whole subtree, tags included, with `RawElement`;
- discard the subtree with `Skip`, which scans it raw: tags, quotes and
  depth only, no attribute indexing and no end-tag matching inside.

`NextChild` is the shape of every decoder: a loop over the children with a
switch on the name, and nothing to do for the elements it does not name.
`Children` is the same loop as a range statement, and `Tokens` is `Next` as
one; both end silently on an error, which `Err` reports after the loop.

```go
for name := range r.Children(r.Element()) {
	switch string(name) {
	case "v":
		v, err := r.ElementText()
		...
	}
}
if err := r.Err(); err != nil {
	return err
}
```

**Keep one `Children` loop per function.** A range-over-func loop costs
nothing only while the compiler inlines the iterator at the call site, and it
stops doing that two or three nested loops in. Measured on the worksheet
above, the typed decode written as four nested `Children` loops in one method
allocates four times per cell and runs at 74 MB/s; the same decode with one
loop per method allocates nothing and runs at 122 MB/s, against 132 MB/s for
the explicit `NextChild` loops. One element type per `DecodeXMLFrom` is the
design anyway, so the rule costs nothing to follow.

```go
func (c *Cell) DecodeXMLFrom(r *xmlro.Reader) error {
	ref, _ := r.Attr("r")
	c.Col, c.Row = parseRef(ref)
	if s, ok := r.Attr("s"); ok {
		c.Style, _ = s.Int()
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
			c.Value, err = v.Float()
		}
	}
}
```

## Values

`Attr`, `Text` and `ElementText` return a `Value`, the bytes as written with
entities still encoded. Its methods decode on demand: `Int`, `Float`, `Bool`
parse the raw bytes, `Equal` compares decoded content, `String` and
`AppendTo` decode into new or caller-owned storage. Content with no
ampersand, which is nearly all of it, is never copied.

`Float` converts a plain decimal of at most 15 significant digits by one
exact division, which is bit-identical to `strconv.ParseFloat` and twice as
fast; anything else, an exponent, more digits, `inf`, goes to `strconv`.

## Lifetime

Every slice the reader returns aliases its buffer and is valid until the next
call that advances the reader: `Next`, `Skip`, `NextChild`, `ElementText`,
`RawElement`, `Decode`. Keep a name or a value by copying it. The reader is
not safe for concurrent use; reuse one across parts with `Reset`.

## Names and namespaces

Names are matched as written, prefix included. Office XML generators emit
canonical prefixes (`w:`, `a:`, `r:`, `mc:`), so `NameIs("w:p")` is enough,
and it costs a byte comparison.

The reader also tracks `xmlns` declarations, so a caller that must tell one
vocabulary from another can ask. `Namespace` returns the namespace of the
current element's name, through its prefix or the default namespace;
`LookupNamespace` resolves any prefix at the current position. Declarations
are copied once, when they are read, which for an Office part is a handful
of strings at the root; every start tag after that pays one byte comparison
per attribute to notice there are none. The `xmlns` attributes stay visible
through `NextAttr` as well.

## On TinyGo

Everything above holds under TinyGo 0.42, with two differences a caller
should know.

**`string(b) == "lit"` allocates.** The Go compiler elides the conversion
in a comparison, a `switch string(b)` and a map index; TinyGo does not, and
copies the bytes every time. Compare names with `NameIs`, values with
`Value.Equal`, and anything else with `xmlro.Equal(b, s)`, which allocates on
neither compiler. This package uses nothing else internally, and the
allocation tests run under `tinygo test` to hold it there.

**The iterators allocate per loop.** TinyGo puts the closure contexts of a
range-over-func loop on the heap, the iterator's and the loop body's, so
the cost grows with what the body captures: 32 bytes for a `Tokens` loop
with an empty body, 80 for a `Children` loop with one, about 200 for a
loop whose body decodes into a struct. It is paid once per loop rather than
per iteration, and the tests pin the numbers. The explicit `Next` and
`NextChild` calls the iterators wrap allocate nothing, so a decoder that
must not allocate on TinyGo writes the explicit loop.

`testing.AllocsPerRun` returns zero under TinyGo whatever the code does, so
the allocation tests here measure `runtime.MemStats.TotalAlloc` instead, and
`testing.B.Loop` is unimplemented there, so the benchmarks use `b.N`.

## Bounds and refusals

- The buffer grows only to hold one token or one capture, never past
  `Options.MaxBufferBytes` (1 MiB by default). A larger token is `ErrTooLarge`.
- Nesting stops at `Options.MaxDepth` (1024 by default).
- A `DOCTYPE` is refused unless `Options.AllowDoctype` is set; an internal
  subset is refused always. There are no external entities to expand.
- An XML declaration naming an encoding other than UTF-8, or a UTF-16 byte
  order mark, is `ErrEncoding`.
- An end tag that does not match its start tag is a `SyntaxError`, checked by
  a 32-bit hash of the name; a malformed attribute is one too. That is the
  whole of the well-formedness checking done: duplicate attributes, the
  characters a name or text may contain, and UTF-8 validity are not checked.
  This is a reader for documents a writer produced, not a validator.
- Decoding normalizes line ends in text (`\r\n` and `\r` to `\n`) as XML
  requires; it does not apply the further whitespace normalization XML
  specifies for attribute values.

Three fuzz targets check that no input panics the reader and that a document
reads identically as a byte slice and as a one-byte-at-a-time stream through
a buffer too small for any token.

## Not in scope

Writing: this reader serves a viewer, which produces nothing. Also
reflection-based mapping, DTDs, and encodings other than UTF-8.
