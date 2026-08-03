---
id: flow:batch-send
type: flow
title: Batch Send and Result Delivery
---
How one api:sql-batch call reaches a native driver, runs, and returns the connection, for both transports in decision:batch-transport-per-driver.

```yaml
flow:
  - id: lease
    actor: api:sql-batch
    action: db.Conn(ctx) to pin one pooled connection, deferred Close
    note: skipped by SendConn, where the caller already owns the conn
    next: lookup
  - id: lookup
    actor: api:sql-batch
    action: find the adapter registered for db.Driver()
    branch:
      hit: raw
      miss: return an unsupported-driver error, never a sequential fallback
  - id: raw
    actor: api:sql-batch
    action: sql.Conn.Raw, entering the only scope where driver.Conn is valid
    next: dispatch
  - id: dispatch
    actor: adapter
    action: build the wire form from the queued statements
    branch:
      postgres: pg_send
      mysql: my_join
  - id: pg_send
    actor: system:pgx
    action: pgx.Conn.SendBatch, one pipelined extended-protocol exchange
    on_error: close_results
    next: deliver
  - id: my_join
    actor: adapter
    action: >
      join with ";", optionally wrapped in START TRANSACTION and COMMIT, then
      ExecerContext.ExecContext in one comQuery
    constrained_by: rule:mysql-multi-statement-batch
    on_error: classify
    next: my_split
  - id: my_split
    actor: adapter
    action: assert the Result to AllRowsAffected and AllLastInsertIds, one entry per statement
    next: deliver
  - id: deliver
    actor: api:sql-batch
    action: hand Results to the caller callback, in queue order
    next: close_results
  - id: close_results
    actor: api:sql-batch
    action: read every unread result and run pending per-query callbacks
    next: classify
  - id: classify
    actor: api:sql-batch
    action: attach the failing statement index when known, per error_attribution
    next: release
  - id: release
    actor: api:sql-batch
    action: leave the Raw callback, then Close the leased conn back to the pool
    note: >
      results must not outlive this step; nothing holding the driver.Conn may
      escape, which is why Results is a callback parameter
round_trips:
  unbatched: one per statement
  postgres: one, plus the prepare exchange the query exec mode implies
  mysql: one, or three with the implicit transaction wrapper
notes:
  - no goroutine is introduced, per rule:tinygo-threads-scheduler
  - context cancellation stays the driver's, unchanged from a single query
```
