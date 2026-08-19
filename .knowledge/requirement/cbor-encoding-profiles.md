---
id: requirement:cbor-encoding-profiles
type: requirement
title: Two Enforceable CBOR Profiles
---
Two named option presets, one per profile system:ebigentserver defines, so a caller names a profile instead of assembling limits by hand. This package enforces a profile it is handed; it never infers one from a message.

```yaml
state: shipped 2026-08-19
shipped:
  api: Wire() and World() return a Profile; Validate, ValidateAppended, DecoderOptions, EncoderOptions, NewReader, ReaderOver
  wire_bounds: 16 levels, 1024 items, 4 KiB strings, 64 KiB input
  accessors: MaxNestedLevels, MaxContainerItems and MaxInputBytes report the bounds
  world_bounds: 32 levels, 65536 items, 4 MiB strings, 64 MiB input and raw
  world_key_order: bytewise, so the length-first default stays passkey's alone
  escape_hatch: AllowingFloats and WithMaxInputBytes return modified copies
  cost: Wire().Validate 23.9 ns/op and World().Validate 90.0 ns/op, both zero allocation
priority: must
defined_elsewhere: >
  system:ebigentserver's cbor-wire-profile and cbor-world-profile concepts own
  the definitions. This repository owns enforcement only.
wire_profile:
  enforces:
    - definite lengths only
    - arrays only, no maps
    - no floats, per requirement:cbor-scaled-integer-support
    - no tags
    - no text keys
    - fixed item count and order per message schema
    - a small nesting bound
  field_names_never_appear: >
    which is what makes partial version compatibility undetectable, and why the
    protocol version is a hard mismatch rather than a negotiation
world_profile:
  enforces:
    - deterministic map key order, see rule:cbor-map-key-ordering
    - no indefinite-length output
    - optional fields and tags permitted
    - larger bounds, for snapshots
  raw_retention: >
    RejectDuplicateMapKeys may hold a whole root item up to MaxRawMessageBytes.
    The default is 1 MiB and a snapshot can exceed it, so the world preset sets
    that bound deliberately rather than inheriting it.
depth_arithmetic:
  what_counts: >
    MaxNestedLevels counts nested containers and a tag is one of them, so a tag
    over an array is two levels
  validate_sums: >
    Validate walks the whole document, so an envelope wrapping a message costs
    the envelope's depth plus the message's
  reading_takes_the_larger: >
    ReadRaw measures a captured item from zero, so decoding an envelope field by
    field costs the larger of the two depths rather than their sum. An envelope
    needing 7 as one document decodes under a bound of 6 that way.
  a_profile_must_hold_patches_of_its_own_messages: >
    a patch or delta carries a subtree of the document it describes, so it is
    always deeper than that document: exactly three levels for the shape
    [base, [[op, [path...], value], ...]]. Measured, a plausible snapshot needs
    5, a patch of it 8, a patch of that 9. The wire bound was 8, which fit
    messages and refused every patch of one, and that failure would have
    surfaced the first time system:ebigentserver generated a delta rather than
    at the boundary where it was introduced. Raised to 16.
  the_number_is_a_protocol_decision: >
    16 is headroom, not a derivation. The bound a profile needs comes from the
    schema it carries and from what wraps it, which is why the accessors exist.
validate_takes_a_profile:
  needed: >
    Validate gains a profile argument, so whether a byte string is legal under a
    profile is answerable without decoding it into anything
  today: >
    no such entry point exists, so passkey builds an Encoder over io.Discard and
    calls WriteRaw for the side effect of its validation, at parse.go:213. A
    validator reachable only through an encoder is the shape to remove.
selection_is_not_ours: >
  which profile a message kind uses is system:ebigentserver's
  profile-selection-by-message-kind rule, and the caller's call
errors: >
  a profile violation gets its own sentinel, leaving
  requirement:error-classification's existing set intact, and a limit refusal
  keeps naming the limit it hit
open: >
  whether RawMessage gains a profile-tagged variant, so a raw item validated
  under the world profile cannot be spliced into a wire-profile message by mistake
open_streaming: >
  whether the world profile needs indefinite-length output for streaming episode
  logs. rule:cbor-determinism-contract forbids it today.
done_when: >
  a wire-profile message round-trips byte-identically on darwin/arm64,
  linux/amd64 and js/wasm, and a float in one is an error at encode and at decode
```
