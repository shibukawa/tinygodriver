---
id: decision:xml-struct-mapping
type: decision
title: XML Struct Mapping Is An Interface Plus Generated Code
---
A struct reads itself from an element through `DecodeXMLFrom(r *Reader) error`, written by hand or emitted by a generator from struct tags. The package ships the interface and the reader; it ships no reflection-based `Unmarshal`.

```yaml
state: proposed 2026-09-24, with the prototype in requirement:xml-office-reader
options_weighed:
  reflection_unmarshal:
    what: encoding/xml's shape, struct tags read at run time
    cost: >
      reflection over every field of every cell, one allocation per string
      field whether or not the caller wanted a string, and a generic walker
      that cannot skip a subtree without first understanding it. Measured
      through the standard library at 11 MB/s; the reflection is most of the
      gap to the 96 MB/s hand-written decoder into the same struct
    tinygo: >
      reflect works there, but a reflection-driven decoder keeps every type's
      metadata alive in the binary, and rule:tinygo-reflect-func-pointer is
      the reminder that its edges differ
    rejected: for the same reason encoding/cbor refused struct tags
  interface_only:
    what: >
      Decodable, plus the reader calls a decoder is made of: Attr, NextChild,
      ElementText, Skip. A decoder is a loop over children with a switch on
      the name
    cost: >
      hand-written code per element type. For the shapes an Office consumer
      reads, cell, row, run, paragraph, shared string, that is a page each
      and the page is where the typed conversion lives anyway: the cell
      reference parsed to a column and a row, the number parsed, the shared
      string kept as an index. A tag cannot say that; the code can
    chosen: as the runtime half, and as all that lives in this repository
  generated:
    what: >
      system:tinybind-go emits DecodeXMLFrom from tags on a struct, the way
      it emits the JSON pair and is asked to emit the CBOR pair in
      requirement:cbor-codec-interface. The emitted body is the hand-written
      shape above with no runtime type switch
    cost: a backend in the other repository, and a tag vocabulary for XML
    chosen: as the convenience half, owned by tinybind, not designed here
  path_subscriptions:
    what: >
      a compiled set of element paths, worksheet/sheetData/row/c, each with a
      callback, matched by name hash at every start tag. The SAX-style filter
      the maintainer described, for a caller that wants three values out of
      a document and no struct
    cost: >
      a callback per match, and the closure allocation under TinyGo that
      requirement:xml-office-reader's pull_not_push describes. Writable over
      the pull reader in a page when a consumer asks; not in the runtime now
    deferred: until a consumer that is not a struct decoder appears
tag_vocabulary_for_the_generator:
  proposed: >
    xml:"c" for a child element, xml:"r,attr" for an attribute,
    xml:",text" for the element's own text, xml:"sheetData>row" for a path,
    matching encoding/xml's spellings so a struct annotated for one works
    with the other
  semantics_the_generator_adds: >
    a field of a Decodable type is one call; a slice field appends; a field
    of a numeric type parses through Value.Int or Value.Float without a
    string; a []byte field copies; a string field is the one allocation the
    caller asked for
  not_here: the vocabulary is tinybind's to fix, since its emitter reads it
precedence: >
  a hand-written DecodeXMLFrom wins over a generated one for the same type,
  as encoding/json resolves the same conflict and as
  requirement:cbor-codec-interface already decided for the sibling codec
what_this_gives_up: >
  decoding a struct nobody wrote a decoder for. That is the standard
  library's job and it still does it; this package is for the parts where
  that job costs more than the work being done
```
