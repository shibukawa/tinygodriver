---
id: system:ebigentserver
type: system
title: Ebigent Server
---
A deterministic game server framework, and the second framework-scale consumer of this repository after system:popcornwave. Its realtime and world paths are why requirement:cbor-package-move exists.

```yaml
import_path: github.com/shibukawa/ebigentserver
state: >
  specification. Its Phase 0 settles numerics and code generation before
  anything else is built, on the stated grounds that adding them later is a rewrite.
uses_from_here: decision:cbor-import-path
owns_its_own_profiles: >
  wire and world are struct literals in that project, not presets in this one.
  requirement:cbor-encoding-profiles supplies the mechanism and Canonical and
  Deterministic, which are standards; an application's subset is the
  application's to name.
what_it_defines_that_this_repo_enforces:
  cbor-wire-profile: the compact subset, fixed-order arrays with no field names
  cbor-world-profile: the evolvable subset, for snapshots and episode logs
  fixed-point-on-wire: scaled integers only, scale carried by the protocol version
  no-float-in-simulation: the property requirement:cbor-numeric-primitives enforces at the codec
  protocol-version-must-match: a hard mismatch rather than a negotiation
  profile-selection-by-message-kind: which profile a message kind uses; not this repository's call
determinism_gate: >
  its Phase 2 replays an episode on arm64 and amd64 and compares digests. That
  is what makes rule:cbor-determinism-contract a property rather than a preference.
codegen: >
  tinybind generates the encoders; see system:tinybind-go. Struct analysis,
  schema derivation, protocol-version derivation and rejecting non-deterministic
  fields at build time are all the generator's.
not_ours: >
  message framing, backpressure, sequence and ack, delta generation, and what a
  message means. This repository supplies primitives and enforcement only.
independent_of_popcornweb: >
  a decision in their catalog, and the reason the codec cannot stay inside
  system:popcornwave
```
