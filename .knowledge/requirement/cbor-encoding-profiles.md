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
  wire_bounds: 8 levels, 1024 items, 4 KiB strings, 64 KiB input
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
