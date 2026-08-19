---
id: rule:cbor-determinism-contract
type: rule
title: CBOR Determinism Contract
---
Properties the encoded form holds on every target, forever. These are enforced rather than documented, because system:ebigentserver gates a phase on replaying an episode on arm64 and amd64 and getting identical digests, and this codec is on that path.

```yaml
state: verified 2026-08-19
verified:
  compiler_parity: >
    the same program encodes to 841904d2200003 under standard Go and under
    TinyGo 0.41.1, and the whole test suite passes under both
  platforms: >
    builds for linux/386, linux/arm, js/wasm, linux/amd64, windows/amd64 and
    darwin/arm64; the test binary also compiles for linux/386 and js/wasm
  int_width_audit: >
    resolved rather than rewritten. MaxContainerItems is an int capped at
    maxSliceLen/2 by normalizeDecoderOptions, so every later container length
    converts to an int in range on both widths. The math.MaxInt64/2 guard that
    implied otherwise is gone, and maxSliceLen names the assumption.
  no_float: enforced at encode and at decode, see requirement:cbor-scaled-integer-support
applies_to: requirement:cbor-encoding-profiles
stable_bytes: >
  the same value encodes to the same bytes on every architecture and every run.
  A change to the encoding is a protocol-version change, which
  system:ebigentserver treats as a hard mismatch rather than a negotiation.
no_float_on_wire: >
  no float reaches the wire profile, at encode or at decode. Enforced on both
  sides; see requirement:cbor-scaled-integer-support.
no_observable_map_order: >
  map iteration order is never observable in output. Deterministic map output
  already sorts; nothing new may introduce an unsorted path. Which order is
  rule:cbor-map-key-ordering.
int_width_audit:
  problem: >
    int is 64-bit on a dedicated server and 32-bit on a js/wasm client, and the
    decoder compares and converts against it
  sites:
    - decoder.go:61, MaxContainerItems > math.MaxInt/2
    - decoder.go:103, n > uint64(math.MaxInt)
    - decoder.go:120, min(int(n), readChunkBytes)
    - decoder.go:377, tok.Length = int(n), guarded only against math.MaxInt64/2
  required: >
    each site is shown to reach the same decision at both widths, or is
    rewritten in terms of int64 and uint64. This package cannot ban int outright
    the way system:fixmath does, because len returns int.
compiler_parity: encoded output is byte-identical between a TinyGo build and a standard Go build
platform_matrix: >
  requirement:platform-matrix must gain js/wasm and one 32-bit target for this
  package. Otherwise the int-width audit is verified in production, by a browser
  client and a native server disagreeing about a length bound.
build_tags: >
  a build tag may select an implementation; it may never select a wire format.
  No cgo and no new module dependency. The !tinygo fuzz tag is the only
  permitted divergence, per requirement:test-strategy.
```
