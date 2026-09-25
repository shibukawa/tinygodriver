---
id: api:xml-reader
type: api
title: XML Reader API
---
Public surface of `encoding/xml`: a pull reader over a stream or a byte slice, a borrowed Value with decode-on-demand accessors, and the Decodable interface that a struct reads itself through.

```yaml
package: github.com/shibukawa/tinygodriver/encoding/xml
state: prototype, under review with requirement:xml-office-reader
construction: |
  func NewReader(src io.Reader, opts Options) *Reader
  func NewBytesReader(data []byte, opts Options) *Reader
  func (r *Reader) Reset(src io.Reader)
  func (r *Reader) ResetBytes(data []byte)

  type Options struct {
      BufferSize     int  // initial, 64 KiB
      MaxBufferBytes int  // bound on one token or one capture, 1 MiB
      MaxDepth       int  // 1024
      AllowDoctype   bool
  }
tokens: |
  type Kind uint8  // None, StartElement, EndElement, Text, CData, Comment, ProcInst, Directive, EOF
  func (r *Reader) Next() (Kind, error)
  func (r *Reader) Kind() Kind
  func (r *Reader) Depth() int
  func (r *Reader) Offset() int64

  func (r *Reader) Name() []byte
  func (r *Reader) NameIs(s string) bool
  func (r *Reader) LocalName() []byte
  func (r *Reader) Prefix() []byte
  func (r *Reader) Text() Value

  func (r *Reader) Attr(name string) (Value, bool)
  func (r *Reader) NextAttr() (name []byte, value Value, ok bool)
elements: |
  type Element struct{ ... }  // a value: the depth of the element entered
  func (r *Reader) Element() Element
  func (r *Reader) NextChild(e Element) (bool, error)
  func (r *Reader) ElementText() (Value, error)
  func (r *Reader) RawElement() ([]byte, error)
  func (r *Reader) Skip() error  // raw scan of the subtree, no indexing, no matching inside

  type Decodable interface{ DecodeXMLFrom(r *Reader) error }
  func (r *Reader) Decode(d Decodable) error
iterators: |
  func (r *Reader) Tokens() iter.Seq[Kind]              // Next, ending at EOF or an error
  func (r *Reader) Children(e Element) iter.Seq[[]byte] // NextChild, yielding each child's name
  func (r *Reader) Err() error                          // what stopped an iterator
values: |
  type Value []byte  // as written, entities included, aliases the buffer
  func (v Value) HasEntities() bool
  func (v Value) AppendTo(dst []byte) []byte
  func (v Value) String() string
  func (v Value) Equal(s string) bool
  func (v Value) EqualFold(s string) bool
  func (v Value) Int() (int64, error)
  func (v Value) Uint() (uint64, error)
  func (v Value) Float() (float64, error)
  func (v Value) Bool() (bool, error)
  func Unescape(dst, src []byte) []byte
errors: |
  var ErrTruncated, ErrTooLarge, ErrTooDeep, ErrDoctype, ErrEncoding, ErrNotStart error
  type SyntaxError struct{ Offset int64; Msg string }
usage: |
  r := xml.NewReader(part, xml.Options{})
  for {
      k, err := r.Next()
      if err != nil { return err }
      if k == xml.EOF { break }
      if k == xml.StartElement && r.NameIs("sheetData") {
          if err := r.Decode(&sheet); err != nil { return err }
      }
  }
positions:
  start_element: >
    Depth counts the element. Element, Attr, NextAttr, NextChild, ElementText,
    RawElement, Skip and Decode all take their meaning from it
  end_element: >
    Depth no longer counts the element. Element here names the parent, so a
    NextChild loop that ended on a child's end tag continues with the parent's
    next child
  self_closing: a StartElement then an EndElement, as encoding/xml reports it, so consumers have one shape
next_child:
  contract: >
    advances to the next direct child of e and reports whether there was one.
    Anything between children is passed over, and a child the caller did not
    consume, or descended into and left, is skipped. The loop ends on the
    EndElement of e however much of each child was read
  why_a_handle: >
    without one, a loop that left a child unconsumed would descend into it on
    the next call. The handle is the depth of the element entered, held on
    the caller's stack, so a nested loop over a child is the same two lines
element_text:
  contract: >
    the element's own character data, entities decoded, children skipped,
    reader left on the end tag. One run with no entity is a slice into the
    buffer; anything else is assembled in a scratch buffer the next call
    reuses. A self-closing element is an empty Value
  why_direct_text_only: >
    an inline string is <is><t>x</t></is>, a rich run is <r><rPr/><t>x</t></r>,
    and the caller wants the <t>, not the concatenation of everything under
    <is>. Concatenating descendants would be right for neither and would hide
    the run boundaries the caller may care about
decodable:
  contract: >
    called with the reader on the element's StartElement, returns with the
    reader on its EndElement. NextChild and ElementText both end there, so a
    decoder written as either is correct by construction; Decode checks the
    postcondition and refuses one that is not
  naming: >
    DecodeXMLFrom, matching DecodeCBORFrom in requirement:cbor-codec-interface
    and tinybind's DecodeJSONFrom, so the three codecs read alike and a
    generator can emit all three
iterators:
  contract: >
    sugar over Next and NextChild with the same positions and the same
    skipping; they end silently on an error and Err reports it after the
    loop. A break from Children leaves the reader on the child's start tag
    with e still open
  one_loop_per_function: >
    a range-over-func body is free only while the iterator inlines at the
    call site, and the inliner stops two or three nested loops in; see
    range_over_func in requirement:xml-office-reader for the measurement.
    Write one Children loop per DecodeXMLFrom, which the design asks for
    anyway
  why_the_name_is_yielded: >
    switch string(name) on the loop variable is the natural body, compiles to
    comparisons with no conversion, and needs no second call into the reader.
    Everything else the body wants, attributes and text, it reads from r
lifetime: >
  every slice aliases the buffer and is valid until the next call that
  advances the reader: Next, Skip, NextChild, ElementText, RawElement,
  Decode. A Value kept across that boundary is a bug the race detector will
  not find, so the rule is stated on the type and on the reader
not_offered:
  namespace_resolution: see requirement:xml-office-reader, namespaces
  reflection_unmarshal: see decision:xml-struct-mapping
  token_struct: >
    no Token value type. Every accessor reads the reader's current state, so
    there is nothing to allocate and nothing to keep by accident
```
