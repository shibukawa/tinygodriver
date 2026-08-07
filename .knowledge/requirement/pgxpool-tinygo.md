---
id: requirement:pgxpool-tinygo
type: requirement
title: Native pgxpool Under TinyGo
---
Shipped 2026-08-07 as `database/pgx/pgxpool`: the native pool runs under tinygo, riding the vendored fork whose pgconn patches already removed the crypto/tls use.

```yaml
priority: raised to must 2026-08-07, host-go users mostly enter through pgxpool
requested_by: system:popcornwave, 2026-08-07; the maintainer raised it same day
state: shipped 2026-08-07
scope_note: >
  the vendored fork already contained the pgxpool sources, see
  decision:postgres-backend-split, and the pgconn patch set of
  rule:pgx-vendoring had already rerouted every crypto/tls use, so the work
  was exposure and verification, not new patches
shipped_shape: >
  database/pgx/pgxpool per api:pgx-native's alias mechanism: upstream
  pgxpool types on host go, the vendored copy on tinygo. ParseConfig
  installs the same per-connection defaults as database/pgx and keeps the
  pool_* dsn parameters
verified: >
  eight cases on both backends against postgres 17 (ping, defaults with
  pool_max_conns, 20 concurrent queries through one pool, Acquire+Batch,
  tx commit/rollback, ErrNoRows identity, cancellation via CancelRequest,
  Stat), and examples/pgxpooldemo under real tinygo 0.41.1: 8 goroutines
  through 4 conns, batch on an acquired conn, cancellation at 610ms
```
