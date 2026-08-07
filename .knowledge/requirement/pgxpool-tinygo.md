---
id: requirement:pgxpool-tinygo
type: requirement
title: Native pgxpool Under TinyGo
---
Future work: give pgxpool the treatment rule:pgx-vendoring gave pgx, so a native pool runs under tinygo; upstream pgxpool touches crypto/tls directly, which requirement:no-crypto-tls-on-tinygo forbids.

```yaml
priority: low, explicitly sequenced after the wasip and sqlite items
requested_by: system:popcornwave, 2026-08-07
interim: their pgxpool backend ships documented as host go only
scope_note: >
  the vendored fork already contains the pgxpool sources, see
  decision:postgres-backend-split; the remaining work is rewiring its tls use
  onto api:tls-upgrade, the same patch set rule:pgx-vendoring records for
  pgconn, plus verification on both compilers
```
