---
id: requirement:cbor-decoder-defects
type: requirement
title: CBOR Defects To Fix During The Move
---
Defects found while reading the package for requirement:cbor-package-move. Each is worth fixing while there is still exactly one consumer to re-verify against.

```yaml
state: all fixed 2026-08-19
fixed:
  reflection: slices.SortFunc and slices.Sort replaced sort.Slice and sort.Strings
  duplicate_detection: >
    canonicalizeKey in dupkey.go replaced the per-key Decoder and the recursive
    string concatenation. 15513 to 3013 ns/op, 19848 to 2008 B/op, 598 to 133
    allocs/op on an eight-key map with nested array keys. It also now catches
    duplicates the old scheme could only catch by luck: non-minimal arguments,
    differently chunked indefinite strings, and maps whose members differ in
    order.
  chunked_reads: >
    readFull reads a chunk at a time and every multi-byte path goes through it,
    with the anti-amplification property still pinned by its original test
  capture: no longer byte at a time, since the capture appends inside readFull
  negative_range: AppendNegative, MarshalNegative and Encoder.WriteNegative
  raw_retention: documented in README.md and bounded by the world profile
  validating_parser: >
    still a second implementation, now deliberately: it is what
    Profile.ValidateAppended and WriteRaw both need
priority: should
not_reflection_free:
  claim: the package doc says reflection-free
  reality: >
    sort.Slice at encoder.go:170 and sort.Strings at decoder.go:833 both go
    through the reflect-based swapper
  fix: >
    slices.SortFunc and slices.Sort, generic, allocation-free and TinyGo-clean.
    The module floor is Go 1.27, so slices is available.
duplicate_key_detection_is_quadratic:
  where: keyFingerprint, decoder.go:770-855
  what: >
    builds Go strings by repeated += concatenation, per key, per nested item,
    recursing through arrays and maps
  exposure: >
    runs under RejectDuplicateMapKeys, which is the mode the README recommends
    for untrusted input
  fix: a bounded hash set over the captured raw key sub-slices, staying inside MaxRawMessageBytes
read_bytes_is_byte_at_a_time:
  where: decoder.go:102-132
  what: >
    calls readByte in a loop, and each call is an io.ReadFull on a one-byte
    array; readChunkBytes is used only as an initial capacity
  fix: >
    read in chunks, preserving what
    TestADeclaredLengthDoesNotBecomeAnAllocation proves
capture_is_byte_at_a_time: >
  decoder.go:83-87 appends to the raw capture one byte per readByte call and
  re-checks MaxRawMessageBytes on each. Same family as the readBytes loop above
  and it compounds it, since ReadRaw drives readByte for every byte of the item.
negative_range_is_write_only:
  claim: README.md offers "the full encoded negative range"
  reality: >
    true on decode, where Token.Argument holds the whole range, and false on
    encode. WriteInt and MarshalInt take int64, so a negative integer below the
    int64 floor can be read and re-emitted through WriteRaw but never constructed.
  fix: a writer taking the raw argument, or a README that says decode only
validating_parser_is_a_second_implementation: >
  encoder.go's deterministicParser re-implements the decoder's structural checks
  so WriteRaw can validate. Two parsers for one grammar is where
  rule:cbor-determinism-contract erodes quietly. It is also the hook
  requirement:cbor-codec-interface needs for foreign-method validation, so the
  fix is to share it, not to delete it.
document_raw_retention: >
  RejectDuplicateMapKeys may retain a whole root item up to MaxRawMessageBytes.
  Bound it per profile in requirement:cbor-encoding-profiles.
fourth_and_largest: rule:cbor-map-key-ordering
```
