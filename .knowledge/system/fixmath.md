---
id: system:fixmath
type: system
title: fixmath Fixed-Point Library
---
A deterministic fixed-point math library, specification-only as of 2026-08-19. It is named here because requirement:cbor-numeric-primitives must serve it with neither package depending on the other.

```yaml
import_path: github.com/shibukawa/fixmath
state: specification; no released implementation
representation: a defined type over int64 with a canonical binary scale
consumed_by: system:ebigentserver, for simulation numerics
dependency_rule:
  cbor_must_not_import_fixmath: >
    a codec bound to one math library cannot serve a game that supplies its own
    core, which fixmath's substitution clause permits
  fixmath_must_not_import_cbor: >
    it is a math library. Go interfaces are structural, so fixmath can declare
    the method requirement:cbor-codec-interface names without importing the
    package that names it.
bans_int_outright: >
  because its width differs between a dedicated server and a browser client.
  This package cannot follow, since len returns int, which is why
  rule:cbor-determinism-contract audits the sites instead.
owns: fixed-point arithmetic and scale conversion, neither of which belongs in a codec
```
