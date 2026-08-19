---
id: requirement:cbor-streaming-api
type: requirement
title: Incremental CBOR Container, Capture And Skip
---
The encoder can only write a container whose children are already fully encoded, and the decoder can only capture a whole root item. Both shapes come from having had one consumer that read attestations and built small COSE keys, and neither survives requirement:cbor-package-move.

```yaml
state: shipped 2026-08-19
shipped:
  container_headers: AppendArrayHeader and AppendMapHeader, plus the whole Append family
  nested_capture: Reader.ReadRaw captures at any depth and borrows from the input
  skip: Reader.Skip, zero allocation, covered for all fifteen item shapes
  peek: Reader.Peek reports the next kind without consuming
  reset: Reader.Reset and Encoder.Reset
  materializing_path_kept: WriteArray and WriteMap are unchanged, and still sort
  composition_at_depth_verified: >
    three levels of foreign type, each carrying its own encoding, round-trip
    byte-identically at zero allocations on both sides
nesting_boundary:
  bounded: Skip, ReadRaw and Profile.Validate, all by MaxNestedLevels
  not_bounded: >
    ReadArrayHeader and ReadMapHeader read one head and return, so the walk is
    the caller's loop and its depth is the caller's to bound. Decoder differs,
    keeping a frame stack and refusing from ReadToken.
  why: >
    a frame stack is the state a Reader does not keep, and keeping one would
    cost an allocation per reader in the path that exists to have none. Untrusted
    input goes through Profile.Validate first; generated code over a fixed schema
    recurses no deeper than the schema.
  pinned: >
    TestTheHeaderAPILeavesNestingToTheCaller asserts the asymmetry in both
    directions, so it cannot drift into being true by accident or false by
    omission
priority: must
evidence_of_the_current_shape:
  encoder_never_emitted_production_bytes: >
    passkey reaches it twice. passkeytest/encode.go is a test helper, and
    parse.go:213 builds an Encoder over io.Discard purely to reuse WriteRaw as a
    determinism check. No production path has ever sent bytes it produced.
  cost_of_materializing: >
    passkeytest/encode.go allocates a bytes.Buffer and a whole Encoder per
    scalar, because WriteArray takes a slice of RawMessage and WriteMap a slice
    of MapEntry. A parent cannot be written until every child is a finished byte
    slice, so allocation and copying scale with tree depth. Affordable for one
    COSE key per login; not for requirement:cbor-allocation-budget.
container_headers:
  needed: write a definite-length array or map header, then write the children in place
  keeps: >
    the materializing WriteArray and WriteMap stay, for hand-written callers and
    for the passkey path already using them
map_ordering_tension:
  problem: >
    WriteMap sorts keys, which requires every key materialized at once. A
    streaming map writer cannot sort.
  resolution: >
    system:tinybind-go knows the field set at generation time, so it emits
    fields already in the order rule:cbor-map-key-ordering selects and no
    runtime sort is needed. A streaming map writer therefore requires its caller
    to supply keys in order, and verifies rather than sorts.
  hand_written_callers: keep the materializing WriteMap, which sorts for them
nested_raw_capture:
  today: >
    ReadRaw refuses at depth. decoder.go:544 rejects whenever rootOpen is set,
    the frame stack is non-empty, or a tag is pending, so it captures one whole
    root item and nothing else.
  needed: >
    capture exactly one sub-item at the current position, bounded by
    MaxRawMessageBytes, leaving the frame stack consistent
  why: >
    requirement:cbor-codec-interface hands a foreign type the bytes of one
    field, and a field is always at depth
skip:
  today: >
    none. The only way past an unwanted item is ReadRaw, which allocates it,
    retains it, and refuses to run at depth anyway.
  needed: advance past exactly one complete item, at any depth, allocating nothing
  why: >
    the world profile is the tolerant-of-schema-change half of
    requirement:cbor-encoding-profiles, and an unknown or absent optional field
    cannot be handled without it
peek: >
  report the next token kind without consuming it, so an optional field is
  detected by dispatch rather than by capturing and rewinding
byte_slice_sink: >
  the other half of this shape, specified by requirement:cbor-allocation-budget
done_when: >
  a nested container encodes with no child materialized as a separate
  RawMessage, an unknown world-profile field is skipped with no allocation, and
  a foreign field's bytes are captured at depth
```
