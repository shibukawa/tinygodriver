---
id: system:pgx
type: system
title: pgx PostgreSQL Driver
---
Maintained third-party PostgreSQL driver used as the base of both paths of decision:postgres-backend-split: unmodified on host go, three-file patch on tinygo.

```yaml
import: github.com/jackc/pgx/v5
version_validated: v5.10.0
used_as: >
  database/sql driver through pgx/stdlib on both paths; stdlib pulls in the
  whole module, so it costs no extra vendoring over calling pgx directly
provides:
  - system:postgres-wire-protocol via pgproto3, 57 files
  - pgtype coverage for numeric, uuid, interval, jsonb, arrays, 49 files
  - Batch, CopyFrom, LISTEN/NOTIFY, its own pool
  - full tls including client certificates and channel binding, host go only
tinygo_gap:
  compile:
    files: pgconn/config.go, pgconn/auth_scram.go, pgconn/pgconn.go
    symbols: crypto/tls stub, crypto/x509, net.Resolver, net.DefaultResolver
    note: no other package in the module touches these
  runtime:
    was: default DeadlineContextWatcherHandler does not cancel on tinygo
    resolved_by: configuration only, see rule:postgres-query-cancellation
extension_points_used:
  Config.BuildContextWatcherHandler: swaps in CancelRequestContextWatcherHandler
  stdlib.OpenDB: takes a pgx.ConnConfig, so the above is reachable from database/sql
  sql.Conn.Raw: yields *stdlib.Conn then *pgx.Conn for Batch, CopyFrom, LISTEN/NOTIFY
  pgconn/ctxwatch: public package, so no fork is needed for cancellation
version_policy: >
  the host path tracks upstream releases; the tinygo fork rebases onto the same
  tag, and the patch should stay build-tag shaped to keep rebases cheap
