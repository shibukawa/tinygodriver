---
id: requirement:xml-office-reader
type: requirement
title: Allocation-Free XML Reader For Office Parts
---
Reading an Office Open XML part through the standard library allocates for every token and reflects for every struct field, and a worksheet is forty thousand cells of the same three elements. The consumer wants a few of those elements and none of the rest, so the reader's job is to skip, not to map.

```yaml
state: prototype 2026-09-24 at encoding/xml, design under review
priority: should
proposed_by: the maintainer, asking for a SAX-style reader with a story for struct mapping
consumer: >
  an Office file viewer. It reads parts and produces none, so the reader is
  the whole of this package: no writer is planned, decided 2026-09-25
shape_of_the_input:
  parts: >
    worksheets (sheetN.xml), shared strings (sharedStrings.xml), document
    bodies (document.xml), styles, relationships. Each is one part of a zip
    package, decompressed on the way in, so the natural source is an io.Reader
    and a whole part in memory is the exception
  what_they_look_like: >
    UTF-8, no DTD, no entities beyond the five predefined ones and numeric
    references, canonical namespace prefixes (w:, a:, r:, mc:, x14ac:), no
    whitespace between elements, and one element shape repeated tens of
    thousands of times with the information in its attributes
  size: >
    a worksheet runs from kilobytes to hundreds of megabytes. A reader that
    holds the part in memory does not survive the large end on TinyGo
what_the_standard_library_costs:
  measured: linux/amd64, a 1.5 MB worksheet of 2000 rows by 20 cells
  token_loop: 25.6 MB/s through RawToken with 364,082 allocations, 20.7 MB/s through Token with 548,110
  unmarshal: 11.2 MB/s into row and cell structs with 839,877 allocations
  where_it_goes: >
    a Name of two strings and an []Attr per start tag, a copied CharData per
    text run, a bufio layer, namespace translation on every name, and
    reflection over the target struct for every field of every cell
budget:
  scanning: >
    the token loop allocates nothing after the buffer is sized, and runs an
    order of magnitude faster than RawToken. Prototype: 245 MB/s, 0 allocations
  decoding: >
    a hand-written decoder into typed cells (reference parsed to column and
    row, number parsed, shared string as an index) allocates nothing in
    steady state. Prototype: 97 MB/s, 0 allocations; the same struct shape as
    the Unmarshal case with its strings kept, 96 MB/s, one allocation per
    string kept
  text_parts: >
    shared strings decode at one allocation per string, which is the string.
    Prototype: 203 MB/s against 15 MB/s for Unmarshal
  pinned_by: >
    TestSteadyStateReadingAllocatesNothing, through testing.AllocsPerRun on
    both the stream and the byte-slice source, as encoding/cbor pins its own
    budget
pull_not_push:
  asked_for: SAX style, callbacks per element
  chosen: >
    a pull reader: Next advances one token, the caller reads what it wants.
    Filtering is Skip, and a decoder for one element is a loop over its
    children with a switch on the name, which composes: a row decoder calls a
    cell decoder at the child's start tag and gets the reader back at its end
  why_not_callbacks: >
    a SAX handler is an interface with one method per token kind, so every
    consumer carries state across calls to know where it is, and skipping a
    subtree is a depth counter in every handler. Under TinyGo a closure that
    captures is heap allocated, so a callback API would spend the allocation
    the reader exists to avoid. A callback layer can be written over the pull
    reader in a page; the reverse is not true
borrow_not_copy:
  what: >
    every name, attribute and text is a slice into the reader's buffer, valid
    until the next call that advances. The caller copies what it keeps, and
    only that
  decode_on_demand: >
    a Value is the bytes as written. Int, Float and Bool parse the raw bytes,
    since numbers carry no entities; Equal compares decoded content; String
    and AppendTo decode into new or caller-owned storage. Text with no
    ampersand, which is nearly all of it, is never copied
  attributes_are_indexed_in_the_scan: >
    the start-tag scanner records where each attribute's name and value sit
    as it passes them, into a table of four ints per attribute reused across
    tags, and Attr compares the asked name against those few entries. This
    replaced a rescan of the tag per ask on 2026-09-25: the typed decode
    went from 104 to 132 MB/s and a pure scan of the same worksheet from 245
    to 228, since the scanner now parses attribute structure for every
    element whether or not one is asked. Skip does not pay it, see below
streaming:
  what: >
    one buffer over an io.Reader, compacted when a token straddles its end,
    grown only when one token does not fit, bounded by MaxBufferBytes. A byte
    slice is the same reader with the caller's slice as the buffer and no
    source behind it, so there is one code path and one set of tests
  pinning: >
    ElementText and RawElement need bytes that were read before the current
    token, so they pin the start of what they hold and compaction keeps from
    the pin. A capture larger than MaxBufferBytes is ErrTooLarge, which is
    the anti-amplification property: no length in the input reserves memory
well_formedness:
  checked: >
    tag nesting, by a 32-bit FNV hash of each open element's name kept on a
    stack, so a mismatched end tag is a SyntaxError and the stack is four
    bytes per level rather than a name
  not_checked: >
    name productions, attribute uniqueness, the character set of text, the
    byte after a closing bracket. A writer produced this document; the reader
    reads what the writer meant
  refused: >
    a DOCTYPE unless AllowDoctype, an internal subset always, an encoding
    other than UTF-8, nesting past MaxDepth. There is nothing to expand and
    nothing to fetch
namespaces:
  decided: >
    names are matched as written, prefix included, since Office generators
    emit canonical prefixes. Resolution is available on demand for the
    caller that must tell a default-namespace name from a bare one, or one
    vocabulary from another under the same prefix, which a viewer reading
    files from several generators eventually meets
  shipped: >
    2026-09-25. Declarations are recorded as start tags are read, one string
    copy each, and dropped as elements close, Skip included. Namespace and
    LookupNamespace scan the stack from the top. Always on: the cost per
    start tag is one byte comparison per attribute, unmeasurable in the
    worksheet benchmarks
struct_mapping: decision:xml-struct-mapping
surface: api:xml-reader
import_path:
  chosen: github.com/shibukawa/tinygodriver/encoding/xml, package xml
  precedent: decision:cbor-import-path put the first codec at encoding/cbor for mirroring the standard library
  cost: >
    a file that imports both this package and the standard one aliases one of
    them. The benchmark file does; a consumer rarely will, since the point of
    this one is not to need the other
  alternative: encoding/xmlscan or encoding/saxml, if the alias is judged a trap
verified:
  host_go: 22 tests, most through four input shapes each, go vet and race clean, on go1.27.0 linux/amd64
  not_yet: tinygo test, which the container lacks; see rule:tinygo-test-constraints for what to expect
range_over_func:
  asked: whether Next should have an iterator form, 2026-09-25
  shipped: >
    Tokens and Children as iter.Seq adapters over Next and NextChild, ending
    silently on an error that Err reports after the loop, as bufio.Scanner
    does. The explicit calls stay the primitives
  measured: >
    the typed worksheet decode written as four nested Children loops in one
    method: 64.8 MB/s and 166,002 allocations, four per cell, because the
    inliner gives up at the third level and every deeper loop body becomes a
    heap closure. The same decode with one Children loop per method: 89.6
    MB/s and 0 allocations, against 98.5 MB/s for the explicit NextChild
    loops. Tokens alone matches Next within 3 percent at 0 allocations
  rule: >
    one Children loop per function. It is free only while the iterator
    inlines at the call site, and one element type per DecodeXMLFrom is the
    design anyway. Stated in the README, and TestIteratorsAllocateNothing
    pins the single-loop case
  tinygo: >
    it compiles and runs, and allocates the closure contexts TinyGo puts on
    the heap, once per loop: 32 bytes for a Tokens loop with an empty body,
    80 for a Children loop with one, 208 for a loop whose body decodes a
    cell, since the body's captures are in the context. Pinned in
    alloc_tinygo_test.go. A decoder that must not allocate under TinyGo
    writes the explicit loop
names_as_bytes:
  asked: whether handling tag and attribute names as []byte rather than string is faster, 2026-09-25
  measured: >
    the same scan of the worksheet with a name switch and one attribute
    lookup per start tag. Names converted to strings on every tag: 164 MB/s
    and 44,000 allocations. Names compared as bytes against literals: 193
    MB/s and none. Names interned through a map: 153 MB/s and none, slower
    than converting because a map probe per tag costs more than a small
    allocation. Against the 244 MB/s floor of a scan that reads no names, a
    string switch on the bytes costs 5 percent and a switch on the name
    hash the reader already keeps costs 3.5 percent
  what_the_gain_is: >
    the allocation and the copy, not the comparison. string(b) == "lit" and
    switch string(b) compile to a length check and a memequal with no
    conversion, so bytes compare as fast as strings do. The saving is that
    nothing is copied for a name the caller only looks at, which in a
    worksheet is every name
  where_a_string_is_still_right: >
    a name the caller keeps. A viewer keeps almost none, since the
    vocabulary is fixed and dispatch is on it; a value it keeps is one
    allocation, through Value.String, and that is the one it asked for
  not_offered: >
    a hash accessor for dispatch. It would save 1.5 percent of a scan and
    move a collision from a mismatched-tag error into silent misdispatch
  the_next_lever: >
    attribute access, not names. The attribute lookup is most of the gap
    between the floor and the byte-compare scan, because Attr rescans the
    tag per ask; see decode_speed under open
  bench: names_bench_test.go, BenchmarkNames_*
  tinygo_caveat: >
    the whole argument that bytes compare as fast as strings rests on the Go
    compiler eliding the conversion, and TinyGo does not: string(b) == s
    there is a 16-byte allocation per comparison. Equal is the exported
    form that allocates on neither, and the package uses nothing else
declared_attributes:
  asked: >
    whether handing the reader the wanted attribute names before Next, so
    the scanner decides in the pass, would be faster, 2026-09-25
  what_it_would_save: >
    the name comparison in Attr, and the indexing of elements nobody asks
    about. Measured ceiling for the first, attribute values read by slot
    with no comparison at all: 143 MB/s against 133 by name, 7 percent,
    which a real declared API would not reach since the scanner must still
    match names to fill the slots. The second is the 7 percent between the
    indexed scan and the old one, paid only on elements that are tokenized
    and not asked, and a viewer skips those as subtrees, where Skip already
    pays nothing
  what_it_would_cost: >
    a per-element-type declaration, worksheet c wants r s t, that every
    decoder registers before reading and that the scanner consults by name
    hash on every start tag; a second way to reach an attribute; and a
    coupling between the reader and the schema that the Decodable design
    keeps out of the reader
  decided: >
    not offered. The rescan was the cost, and indexing in the scan removed
    it without the caller declaring anything; what a declaration could add
    is under 7 percent at the ceiling
float_parsing:
  asked: why strconv.ParseFloat is slow, 2026-09-25
  measured: >
    32 ns for "12.25", 30 for "4000", 70 for a 17-digit value, 74 for a
    20-digit one that leaves the Eisel-Lemire path, 97 ns and two
    allocations for a malformed one; ParseInt on "4000" takes 16
  where_it_goes: >
    readFloat, the syntax scanner, is 70 percent of it. It accepts signs,
    hex floats, underscores, three exponent markers, inf and nan in several
    spellings, and tracks truncation past 19 digits, one branchy byte at a
    time. The conversion after it is cheap: an exact float64 division for
    15 digits or fewer, Eisel-Lemire's 128-bit multiply up to 19, and the
    big-decimal fallback only past that. The generality is the cost, and
    the correctly rounded answer for every input is what it buys
  shipped: >
    Value.Float tries Clinger's fast path first: a plain decimal of at most
    15 significant digits and 22 fraction digits is one exact division,
    bit-identical to strconv, and everything else falls back. 15.6 ns
    against 32; the typed decode moves from 98.5 to 104 MB/s, in line with
    its 9 percent share. TestFloatFastPathAgreesWithStrconv compares
    200,000 generated decimals bit for bit
  fallback_cost: >
    103 ns for a value the fast path declines, the wasted scan plus
    strconv. Exponent forms are rare in cell values, so the average holds
open:
  skip_speed: >
    shipped 2026-09-25: Skip scans raw, tracking tags, quotes, comments,
    CDATA and depth, with no attribute indexing and no end-tag matching
    inside the subtree, and leaves the reader on the end tag with Name set.
    259 MB/s over the worksheet body against 228 for the token scan; the
    gap is small because the tags are small and the per-byte tag walk
    dominates either way. What it buys is that the attribute indexing above
    is never paid for a subtree nobody reads
  decode_speed: >
    decoding runs at 40 percent of scanning speed. Profiled 2026-09-25 on
    the typed worksheet decode: Attr was 28 percent of the time, the token
    scan 43, ParseFloat 9. The attribute index in the scan and the float
    fast path took both; the decode now runs at 132 MB/s, 58 percent of the
    scan, and the remaining cost is the token scan itself
```
