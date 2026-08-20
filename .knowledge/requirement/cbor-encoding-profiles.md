---
id: requirement:cbor-encoding-profiles
type: requirement
title: Profiles Are Format Restrictions, Not Limit Sets
---
A Profile is a named subset of CBOR that both ends of a protocol agree on. It carries no resource limits, because those answer to a different owner, and it names no application's subset, because every consumer names its own.

```yaml
priority: must
state: reshaped 2026-08-20; first shipped 2026-08-19 with both halves in one object
the_split:
  a_profile_belongs_to_the_protocol: >
    both peers must agree on it, a disagreement is a defect, and changing it
    changes the format
  a_limit_belongs_to_the_deployment: >
    how large an item a process will read is that process's business. A
    dedicated server and a js/wasm client hold different answers and there is
    nothing to agree on.
  so_they_are_passed_together_not_bundled: >
    Validate(data, opts), NewReader(data, opts), ReaderOver(data, opts).
    Bundling them made a deployment decision look like a protocol change and hid
    a protocol change inside a deployment one.
  what_it_cost_before_the_split: >
    the nesting bound was a stack concern carried as a format assertion, so the
    wire profile fit its own messages and refused patches of them. That is the
    same defect twice: see rule:cbor-determinism-contract.
shape:
  fields: Name, RequireSortedKeys, KeyOrder, RejectMaps, RejectTags, RejectFloats, RejectIndefinite, RejectTextKeys
  zero_value: restricts nothing; every well-formed CBOR item is legal under it
  building_one: a struct literal, so a consumer needs nothing from this package to name its subset
presets_are_standards_only:
  Canonical: CTAP2 canonical CBOR and COSE; length-first keys, no indefinite lengths, floats permitted
  Deterministic: RFC 8949 section 4.2.1; the same with bytewise keys
  refused_as_presets: >
    wire and world. They are system:ebigentserver's subsets, defined by its own
    concepts, and a codec that ships one application's restrictions is claiming
    an authority it does not have. They are struct literals in that project now,
    and this package's tests carry them as the worked example.
float_is_not_a_policy: >
  RejectFloats is opt-in. Floats are ordinary CBOR here, supported at encode and
  decode with shortest-form output and one canonical NaN; see
  requirement:cbor-numeric-primitives. A format that carries scaled integers
  switches it on and gets ErrFloatRefused on both sides.
enforcement_is_all_it_does: >
  which profile a message kind is read under is the schema's and the caller's.
  This package never infers one.
found_by_fuzzing_the_reshape:
  indefinite_head_on_a_scalar: >
    Validate accepted 0x1f, an integer head claiming an indefinite length, which
    Skip had always refused. Every preset used to refuse indefinite lengths
    before the check was reached, so a zero-value profile is what made it
    reachable. A profile permitting indefinite lengths permits the legal ones;
    it does not waive well-formedness.
  see_also: requirement:cbor-error-diagnostics for the second finding
done_when: >
  a consumer defines its subset without this package knowing about it, the same
  profile under two limit sets gives the same format verdict, and the same
  limits under two profiles give the same limit verdict
```
