# encoding/xml

A pull reader for Office Open XML parts: worksheets, shared strings, document
bodies, styles. It reads one token at a time from an `io.Reader` or a byte
slice, hands back names, attributes and text as slices into its own buffer,
and allocates nothing in steady state. A caller copies only what it keeps.

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
| `Decoder.RawToken` loop | 59,324,868 | 25.6 | 364,082 |
| `Decoder.Token` loop | 73,473,628 | 20.7 | 548,110 |
| **`Reader.Next` loop** | **6,190,569** | **245.0** | **0** |
| `Unmarshal` into row/cell structs | 135,890,580 | 11.2 | 839,877 |
| **`Decodable` into the same structs** | **15,839,326** | **95.8** | 75,819 (the strings kept) |
| **`Decodable` into typed cells** | **15,584,072** | **97.3** | **0** |
| `Decodable` into typed cells, `Children` loops, one per method | 16,927,753 | 89.6 | 0 |
| `Decodable` into typed cells, four `Children` loops in one method | 23,418,110 | 64.8 | 166,002 |

And on a shared-strings part, 20,000 strings with entities and rich-text runs:

| | MB/s | allocs/op |
|---|---|---|
| `Unmarshal` | 15.0 | 436,058 |
| **`Reader`, one string per `<si>`** | **203.3** | **20,000** |

`go test -bench . ./encoding/xml` reproduces the table.

## Reading

```go
r := xml.NewReader(part, xml.Options{})   // or xml.NewBytesReader(data, ...)
for {
	k, err := r.Next()
	if err != nil {
		return err
	}
	if k == xml.EOF {
		break
	}
	if k == xml.StartElement && r.NameIs("sheetData") {
		err = r.Decode(&sheet)          // sheet implements Decodable
	}
}
```

On a `StartElement` the caller can:

- read `Name`, `LocalName`, `Prefix`, and any attribute with `Attr(name)` or
  all of them with `NextAttr`;
- walk the direct children with `NextChild`, which skips whatever the caller
  does not consume;
- take the element's own text with `ElementText`, entities decoded, children
  skipped;
- capture the whole subtree, tags included, with `RawElement`;
- discard the subtree with `Skip`.

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
allocates four times per cell and runs at 65 MB/s; the same decode with one
loop per method allocates nothing and runs at 90 MB/s, against 98 MB/s for
the explicit `NextChild` loops. One element type per `DecodeXMLFrom` is the
design anyway, so the rule costs nothing to follow.

```go
func (c *Cell) DecodeXMLFrom(r *xml.Reader) error {
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

## Lifetime

Every slice the reader returns aliases its buffer and is valid until the next
call that advances the reader: `Next`, `Skip`, `NextChild`, `ElementText`,
`RawElement`, `Decode`. Keep a name or a value by copying it. The reader is
not safe for concurrent use; reuse one across parts with `Reset`.

## What is matched

Names are matched as written, prefix included. Office XML generators emit
canonical prefixes (`w:`, `a:`, `r:`, `mc:`), so `NameIs("w:p")` is enough,
and it costs a byte comparison. Namespace resolution is not performed; the
`xmlns` attributes are ordinary attributes a caller can read.

## Bounds and refusals

- The buffer grows only to hold one token or one capture, never past
  `Options.MaxBufferBytes` (1 MiB by default). A larger token is `ErrTooLarge`.
- Nesting stops at `Options.MaxDepth` (1024 by default).
- A `DOCTYPE` is refused unless `Options.AllowDoctype` is set; an internal
  subset is refused always. There are no external entities to expand.
- An XML declaration naming an encoding other than UTF-8 is `ErrEncoding`.
- An end tag that does not match its start tag is a `SyntaxError`, checked by
  a 32-bit hash of the name, which is the whole of the well-formedness
  checking done. This is a reader for documents a writer produced, not a
  validator.

## Not in scope

Writing: this reader serves a viewer, which produces nothing. Also namespace
resolution, reflection-based mapping, and DTDs.
