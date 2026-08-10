---
id: decision:configuration-resolution-boundary
type: decision
title: Resolve Configuration Once At The Runtime Boundary
---
Public configuration remains an input model; constructors resolve it once into an immutable runtime model.

```yaml
state: accepted
accepted_on: 2026-08-10
implemented_on: 2026-08-10
problem: >
  writable raw values, aliases, defaults, and derived runtime values coexist in
  several Config objects. Later mutation can leave an older derived value active.
rules:
  - one semantic choice has one canonical input representation
  - environment, defaults, aliases, and named registrations resolve in one function
  - resolution returns a new internal value and never writes derived state into public input
  - runtime code consumes only the resolved value
  - serialization reads raw input, never derived runtime state
  - conflicting alternatives fail during resolution; option order does not choose silently
  - resolved values are immutable after first use
ownership: decision:http-client-policy-ownership
parity: requirement:configuration-semantics-parity
applies_to:
  - decision:mysql-config-resolution
  - decision:datastore-option-exclusivity
  - api:https-transport
  - api:pgx-native
migration:
  compatibility: keep exported fields and option names unless correctness requires rejection
  sequence: add resolver and tests, route constructors through it, then remove internal duplicates
implementation: shipped across the scoped adapters and client constructors
```
