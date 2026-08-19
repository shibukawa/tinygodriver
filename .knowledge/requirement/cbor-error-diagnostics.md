---
id: requirement:cbor-error-diagnostics
type: requirement
title: CBOR Errors Carry A Position
---
A decode failure today names a sentinel and the limit it hit, never a place. That is enough to reject an attestation and not enough to diagnose a desync between two builds of a game.

```yaml
state: shipped 2026-08-19
shipped:
  type: >
    Error{Offset, Path, Err}, wrapping the existing sentinels so errors.Is is
    unaffected, with errors.As reaching the position
  offset: >
    carried by every Reader and Profile refusal, positioned at the item that
    failed rather than at wherever the caller started reading. Each recursion
    level wraps its own start and an already-positioned error passes through, so
    the innermost frame is the one that names the offset.
  path: >
    a route in the shape of diagnostic notation: [i] indexes an array, {k}
    selects a map value under key k, {#i key} names the i-th key of a map.
    "cbor: malformed input: reserved additional information 28 (at byte 5, at
    [2][1])" is a whole message.
  how_it_stays_free: >
    nothing tracks a route while decoding succeeds. The route is built after the
    fact, by walking the input again from the start to the offset that failed.
    A decoder maintaining one would pay on every item of every message; a
    failure is not the steady state and can afford one extra walk.
    TestTheRouteCostsNothingWhenNothingFails pins that.
priority: should
today: >
  the sentinels are wrapped with a short label such as "input bytes" or "map
  pairs". Nothing carries an offset, and nothing names a field.
needed:
  byte_offset: the position in the input at which the failing item began
  path: >
    the container path where one is known, as array index and map key, so a
    generated decoder reports which field rather than which byte
  compatibility: >
    errors.Is on the existing sentinels keeps working and the position is
    carried alongside, as requirement:error-classification does for TLS
why: >
  a desync between a server and a js/wasm client is the failure
  rule:cbor-determinism-contract exists to prevent. When one happens anyway the
  two sides are compared byte by byte; an offset turns that into a lookup.
cost:
  offset: >
    already available, since the decoder counts bytes read to enforce
    MaxInputBytes
  path: >
    not tracked today, and the part that must cost nothing in the steady state
    of requirement:cbor-allocation-budget
```
