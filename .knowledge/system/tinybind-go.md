---
id: system:tinybind-go
type: system
title: tinybind-go and firestorebind
---
A struct-binding code generator whose `firestorebind` backend sits between system:popcornwave and `nosql/datastore`. It is the layer that actually calls this driver, so its reports arrive measured rather than estimated, and its constraints are what turn a driver gap into a request.

```yaml
import_path: github.com/shibukawa/tinybind-go
relationship: >
  popcornwave's five stores reach the driver through firestorebind, so a report
  filed by popcornwave is usually about a shape firestorebind generates. This
  concept exists because the 2026-08-05 round came from firestorebind directly,
  from wiring Client.MutationSize into it.
why_it_cannot_hold_local_numbers: >
  a rule in their own catalog, firestorebind-driver-passthrough, forbids a local
  constant for a figure the driver owns. That is what made a 512-byte overhead
  constant a defect rather than a workaround, and why replacing it with a
  smaller, more accurate local constant would have been the same mistake at
  higher precision. A number the driver knows has to come from the driver.
reports_2026_08_05:
  against_this_repository:
    - >
      requirement:datastore-client-scope's out_of_scope still excluded SUM and
      AVG, twenty lines below the entry recording that they were added. Fixed,
      and the guard in requirement:datastore-doc-accuracy extended to reach it.
    - >
      the same file's single_use_state read as unimplemented after
      requirement:datastore-single-use-transaction shipped. Reworded.
    - requirement:datastore-commit-envelope, the only one of the three needing code
  explicitly_not_asking_for:
    - any change to the single-use fold, which they report as working and wider than they scoped it
    - >
      a TTL surface. Their downstream wants a declaration-only ttl tag that
      applies and encodes nothing, so it needs nothing here.
    - system:popcornwave's stale pin, noted rather than filed
open_question_not_ours: >
  whether a Datastore-mode TTL policy requires the target property to be
  indexed, since a ttl field also marked noindex would never expire. Neither
  side has confirmed it and neither is guessing. It bears on their tag, not on
  this driver, which exposes no TTL surface.
value_of_this_reporter: >
  three rounds now where the filed diagnosis survived checking. The commit
  envelope arrived with a measured table that reproduced exactly, and the
  suggested shape, a count-taking overhead rather than a whole-batch size, was
  the right one for the reason they gave.
request_2026_08_07_rows_interface:
  status: >
    filed by system:popcornwave as the single blocker of their pluggable sql
    backend chain; against tinybind-go's sqlbind, not this repository
  problem: >
    sqlbind.Querier.QueryContext returns the concrete *sql.Rows, a struct with
    unexported fields only database/sql can construct, so an executor outside
    database/sql (pgxpool) cannot satisfy Querier. Execer needs nothing:
    sql.Result is already an interface.
  exposure:
    - "sqlbind/statement.go:28 Querier.QueryContext returns *sql.Rows"
    - "sqlbind/rows.go:13 ForEach takes *sql.Rows"
    - "sqlbind/sql.go:9 ScanRows[T] takes *sql.Rows"
    - "sqlbind/registry.go:19 RegisterScanRows[T] takes func(*sql.Rows)"
  requested_shape: >
    one 4-method Rows interface, Next() bool / Scan(...any) error / Err() error
    / Close() error, which *sql.Rows already satisfies; Querier returns Rows
    and the three functions above take it
  evidence: >
    generated many-query code calls only Close, Next, Scan and Err, measured
    from examples/todo/popcornwave/queries/todos_pw_gen.go
  compatibility:
    runtime: sql.DB, sql.Conn and sql.Tx satisfy Querier unchanged
    generated_code: no regeneration needed
    source: >
      only callers that annotated the return as *sql.Rows break; a
      minor-version breaking change
    pgx_close: >
      pgx.Rows.Close returns nothing; their adapter wraps it to return error,
      so the interface keeps Close() error
  refused_by_requester: >
    a second row-returning interface, which would fork generated code per
    backend against their tinybind-sql-runtime decision
consumes: api:datastore-client
downstream: system:popcornwave
