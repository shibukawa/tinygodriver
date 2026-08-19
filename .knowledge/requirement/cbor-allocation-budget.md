---
id: requirement:cbor-allocation-budget
type: requirement
title: CBOR Allocation And Throughput Budget
---
The package was written for one attestation per login. requirement:cbor-package-move puts it on every input of every player of every tick, and the current shape does not survive that.

```yaml
state: shipped 2026-08-19
measured_darwin_arm64:
  note: go test -bench with -benchtime 3s; shorter runs read high on warm-up
  encode_append: 9.2 ns/op, 0 allocs
  encode_materializing: 100.4 ns/op, 5 allocs, the shape it replaced
  decode_reader: 42.4 ns/op, 0 allocs
  decode_streaming: 168.0 ns/op, 7 allocs, the shape it replaced
  validate_wire: 23.9 ns/op, 0 allocs
  skip_unknown_field: 24.2 ns/op, 0 allocs
shipped:
  zero_alloc_test: >
    TestFixedShapeMessageIsZeroAllocationInSteadyState pins encode and decode of
    a four-field wire message at zero allocations through a reused buffer and a
    reused Reader
  argument_reads: >
    a scratch array on the Decoder, because a local one handed to io.ReadFull
    escaped and put an allocation on every head byte carrying an argument
priority: must
byte_slice_entry_points: >
  encode by appending into a caller-owned byte slice, decode from a byte slice
  with no io.Reader wrapping. The io.Reader decoder stays, for passkey and for
  episode logs.
append_head_allocates:
  what: appendHead(nil, ...) on every primitive write
  where: encoder.go, eleven call sites across the Write and Marshal entry points
  fix: every one takes a destination slice
container_shape_is_the_larger_cost: >
  requirement:cbor-streaming-api. Removing the appendHead allocations still
  leaves WriteArray and WriteMap demanding every child as a finished
  RawMessage, which is where the depth-proportional allocation actually lives.
reusable_objects: >
  a Reset on each side, so a session keeps one encoder and one decoder per
  connection instead of one per message
steady_state_zero_alloc: >
  a fixed-shape wire message encodes and decodes with zero allocations in steady
  state, gated by a testing.AllocsPerRun test. The package already pins an
  allocation property in a test rather than in a comment.
borrowed_strings: >
  Token.Bytes and Token.Text allocate per string. The byte-slice decoder should
  offer a borrow-from-input mode with a documented lifetime; the reader decoder
  keeps copying.
benchmarks: both profiles, both compilers, as the evidence that any of this paid
must_not_regress: >
  a declared length never becomes an allocation. That is the anti-amplification
  property allocation_test.go pins, and it exists because this decoder reads
  unauthenticated attestations.
```
