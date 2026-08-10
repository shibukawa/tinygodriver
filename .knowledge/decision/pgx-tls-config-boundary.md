---
id: decision:pgx-tls-config-boundary
type: decision
title: Keep The pgx TLS Boundary Explicit And Exhaustive
---
Vendored pgx keeps its compatibility type, but conversion to `https.Config` must be one tested adapter.

```yaml
state: accepted
accepted_on: 2026-08-10
implemented_on: 2026-08-10
constraint: rule:pgx-vendoring keeps local patches small and upstream-shaped
decision:
  retain: pgconn.TLSConfig and its per-attempt Host
  adapter: one function converts pgconn.TLSConfig to data:https-config
  forbid: field-by-field conversion at call sites
coverage:
  - every supported verification, CA, SNI, and version field is mapped
  - every intentionally unsupported https field is named and tested
  - adding a field to either type forces an adapter test update
reason: >
  direct type reuse would widen the vendored patch and mix PostgreSQL host-attempt
  state with reusable TLS policy; an explicit adapter preserves the boundary
implementation: toHTTPSConfig is the sole adapter and its field-inventory test fails when either public shape changes
related:
  - api:tls-upgrade
  - api:pgx-native
  - decision:configuration-resolution-boundary
```
