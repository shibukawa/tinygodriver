---
id: requirement:sql-batch-execution
type: requirement
title: Batched SQL Execution Across Drivers
---
One `database/sql`-shaped way to send several statements per network round trip, served by a driver-specific transport underneath and shaped after the pgx `Batch` api, so caller code does not fork per database.

```yaml
priority: should
state: p1 shipped 2026-08-03
package: database/sql/sqlbatch
import: github.com/shibukawa/tinygodriver/database/sql/sqlbatch
surface: api:sql-batch
transport: decision:batch-transport-per-driver
motivation:
  problem: >
    database/sql exposes no batch verb, so N statements cost N round trips. On
    tinygo that is N native TLS record exchanges over system:tinygo-netdev, where
    a blocking read holds the scheduler per rule:tinygo-threads-scheduler
  today:
    postgres: >
      api:pgx-raw-conn reaches pgx.Conn.SendBatch on both compilers as of
      2026-08-02. It is pgx-shaped by construction, so it serves postgres callers
      and no one else
    mysql: no batch path on either compiler
    portable_code: none; that gap is what this requirement is for
    note: >
      the postgres half of the value now exists without this package. What
      remains is mysql, and one shape that covers both
  shape_source: system:pgx Batch, because it is the only mature design in reach
supported_drivers:
  postgres:
    transport: pgx SendBatch pipelining
    exec: shipped
    query: >
      shipped. pgx already delivers rows per queued query, so withholding them
      would have cost postgres capability for a mysql-side limitation
    registered_by: pgxstdlib/batch.go, which must live there to name pgx types
  mysql:
    transport: multiStatements, subject to rule:mysql-multi-statement-batch
    exec: shipped, per statement, with RowsAffected and LastInsertID
    query: >
      rejected with UnsupportedError, deferred to p2; see rows_layer in
      decision:batch-transport-per-driver
    registered_by: mysql/batch.go, which needs no build tag: it reaches everything
      through database/sql/driver interfaces and one structural assertion
  sqlite:
    transport: sequential, one statement at a time in one transaction
    exec: shipped
    query: shipped
    registered_by: sqlite/batch.go via RegisterSequential, no adapter at all
    why_default_on: >
      there are no round trips to lose, so the objection that gates
      WithFallback does not apply. What it buys instead is the fsync each
      autocommit statement pays: 200 inserts to a file go from ~50ms to ~1ms on
      all three backends, measured
    bonus: >
      executing separately gives per-statement RowsAffected and LastInsertID and
      an exact failing index, both better than the mysql path manages, and it
      keeps the batch off multi-statement SQL, which the three sqlite backends
      do not agree on
capability_reporting:
  contract: >
    an unsupported combination returns UnsupportedError naming the driver, the
    missing capability, and where possible the fix. It never degrades silently
  why_no_fallback: >
    running the statements one at a time on refusal would report success while
    costing N round trips instead of one, and would drop the all-or-nothing
    guarantee a working driver gives. Refusing keeps a batch's cost something a
    caller can reason about; the caller writes the fallback, because only they
    know whether N round trips are acceptable and whether the statements now
    need a transaction of their own
  three_shapes:
    no_adapter: any third-party driver; Capability is "batch"
    missing_capability: a queued Query on mysql; the batch is refused whole
    wrong_dsn: mysql without interpolateParams; Hint names the setting
  with_fallback:
    what: WithFallback turns a refusal into sequential execution
    opt_in_because: >
      the cost goes from one round trip to one per statement. A caller who
      batched for speed must not get that silently; the semantics are unchanged
    safe_because: >
      a refusal happens before any statement reaches the server, so the retry
      cannot double-apply. That is the adapter contract, and mysql asserts it
      with a batch whose writes would land twice if it broke
    not_needed_for_sequential_drivers: >
      sqlite registered for the sequential path outright, so it is already at
      its best shape and needs no opt-in
  executes_nothing: >
    all three refuse before any statement reaches the server, so there is no
    partial effect to undo. The mysql ErrSkip cases return before the write, and
    a missing multiStatements makes the joined statement a parse error
  documented_in: the sqlbatch package doc and README, both with the errors.As pattern
degradation_boundary:
  rule: >
    round-trip count is allowed to differ per driver and per statement mix,
    because that is what an optimization does. Observable behaviour is not: see
    failure_semantics
  one_documented_exception:
    what: reads in a batch do not necessarily share a snapshot
    why: >
      postgres takes a fresh one per statement at READ COMMITTED, while sqlite
      and mysql innodb share one across the transaction the batch runs in
    resolution: >
      nothing in this package can reconcile that, so the batch promises only
      order, stop-at-first-error and rollback. Stated in the package doc and
      README rather than papered over
must:
  - one public api that compiles and behaves the same under tinygo and host go
  - queue statements with arguments and read per-statement results in order
  - report which queued statement failed, per error_attribution below
  - refuse a driver with no registered adapter rather than guessing a transport
  - leave sql.DB, sql.Conn and sql.Tx untouched; add no driver-level api
should:
  - match pgx naming so a pgx user needs no translation
  - keep atomicity identical across drivers by default, see atomicity
  - split an oversized batch, or refuse it with a sized error, see size_limits
out_of_scope:
  - COPY and LOAD DATA LOCAL INFILE; bulk ingest is a separate shape, not a batch
  - LISTEN/NOTIFY, still reached through sql.Conn.Raw
  - batching across connections or any implicit concurrency
  - multi-row INSERT ... VALUES rewriting, which callers can already write by hand
access_mechanism:
  chosen: sql.Conn.Raw plus an adapter registry keyed on db.Driver()
  why_registry_not_type_sniffing: >
    the raw driver.Conn type differs per build tag on both drivers, and the
    postgres one is *stdlib.Conn either from upstream pgx or from the vendored
    fork per rule:pgx-vendoring. Each backend package registers its own adapter
    in a build-tagged file, so no shared file ever names a tag-dependent type
  why_not_a_driver_wrapper: >
    wrapping driver.Conn to add a method means forwarding every optional
    database/sql interface, and one missed forward silently degrades pooling or
    cancellation
  callback_scoped:
    rule: the whole send-and-read cycle runs inside the Raw callback
    not_an_iteration_limit: >
      streaming with Next works normally inside the callback. What is forbidden is
      letting Rows or Results escape it
    why: >
      Conn.Raw holds dc.Mutex only for the duration of f. A Rows read after f
      returns runs unlocked, so a database/sql query on the same sql.Conn can
      interleave with a half-read result set on the same socket and desync the
      protocol. A panic in f releases the conn as ErrBadConn, leaving the escaped
      Rows reading a discarded connection
    pgx_agrees: >
      pgx already delivers batch rows through QueuedQuery.Query(fn), so adopting
      the callback shape costs no divergence. Only SendBatch returning
      BatchResults has to be folded into Send
    escape_option: >
      materializing every row before returning would make Results escapable, at
      the cost of holding the whole result in memory. Rejected as the default on
      an embedded target; revisit as a separate Collect helper if asked for
    no_goroutine_bridge: >
      feeding rows out over a channel from a goroutine inside f is the usual
      trick, and is refused by rule:tinygo-threads-scheduler
  no_tx: >
    sql.Tx has no Raw, so a batch cannot run inside a database/sql transaction.
    Transaction control is queued as statements instead
failure_semantics:
  contract: >
    one batch stops at the first failing statement, and nothing it did survives.
    This is the observable behaviour every adapter must deliver, whatever its
    transport. Round-trip count may vary per driver; semantics may not
  postgres_is_the_reference:
    mechanism: >
      pgx sends every queued query then one Sync. The backend skips all remaining
      messages after an error until that Sync, and everything between the first
      message and Sync is one implicit transaction, so the whole batch rolls back
    later_statements: never execute, not even the independent ones
    attribution: >
      pgx latches the error on batchResults, so every later Exec or Query returns
      the same error. The first result that fails is the failing statement index
    explicit_begin: >
      queueing BEGIN and COMMIT changes nothing useful. An error still skips the
      queued COMMIT, and Sync ends the aborted transaction as a rollback
    simple_protocol_mode: >
      QueryExecModeSimpleProtocol joins the batch into one Query message, which
      postgres also runs as a single transaction, so the two modes agree
    not_the_same_as_an_explicit_transaction:
      parse_before_execute: >
        the extended-protocol modes may Parse every queued query before any
        executes, so DDL followed by use of what it created fails inside a batch
        where it would succeed inside BEGIN and COMMIT. pgx documents this on
        SendBatch. It is the sharpest difference and belongs in the package doc
      non_transactional_statements: >
        an implicit transaction is still a transaction block, so VACUUM, CREATE
        DATABASE and CREATE INDEX CONCURRENTLY are rejected in any batch of more
        than one statement
      isolation_level: >
        it cannot be set for an implicit transaction; a batch needing REPEATABLE
        READ or SERIALIZABLE must queue an explicit BEGIN carrying it
      session_is_left_clean: >
        Sync closes the aborted transaction, so the connection returns to the pool
        usable. An explicit transaction left open after an error would not
  mysql:
    native: >
      multiStatements is not transactional. Statement 1 commits under autocommit,
      statement 2 fails, statement 3 never runs, and the effect of 1 stands
    resolution: wrap in START TRANSACTION and COMMIT so the contract holds
    caveat: DDL forces an implicit commit, so a batch containing DDL cannot honour it
  decomposed_paths: >
    an adapter that executes statements one at a time still owes the contract:
    open a transaction, stop at the first error, roll back. Sequential execution
    is a round-trip decision, never a semantics decision
  opt_out: >
    WithoutImplicitTransaction gives up the contract deliberately. It must be the
    caller's explicit choice, never the driver's
error_attribution:
  postgres: pgx names the failing queued query exactly
  mysql: >
    one error arrives for the whole comQuery. The index is inferred from how many
    result sets were consumed, which is best effort and unavailable when the
    driver returns an error instead of a Result
  requirement: the error carries the index when known and says so when not
size_limits:
  mysql: >
    interpolateParams checks the joined statement against maxAllowedPacket and
    returns driver.ErrSkip when it does not fit, so the batch has a byte ceiling
    the caller cannot see
  postgres: bounded by the send buffer, not by a protocol limit
  action: measure the practical ceiling before choosing between splitting and refusing
open_questions:
  package_name: sqlbatch, or batch under database/sql; sqlbatch avoids batch.Batch
  mysql_query_route: >
    which of the two routes in rows_layer of decision:batch-transport-per-driver
    p2 takes. The sql.Rows route is far cheaper but cannot report per-statement
    exec results in a batch that mixes writes and reads
  mysql_atomicity_default: >
    implicit START TRANSACTION matches pgx but surprises a caller reading the sql
    they wrote. Confirm before implementing
phases:
  p1:
    what: >
      registry, adapters, Exec on both drivers, plus Query and QueryRow on
      postgres where pgx already provides them
    state: not started
  p2:
    what: Query and QueryRow on mysql, once mysql_query_route is settled
    state: not started
  p3:
    what: sqlite sequential fallback, for api portability only
    state: not started
verified_p1:
  suites:
    postgres: >
      five cases in an external test package, on both backends against postgres
      17: per-statement exec tags, a mixed Query and QueryRow batch with column
      names, rollback plus index attribution on a UNIQUE violation, connection
      release with MaxOpenConns=1, and the empty and nil batch
    mysql: >
      six cases on both backends against mysql 8.4: per-statement RowsAffected
      and increasing LastInsertID, rollback on a UNIQUE violation with Index -1,
      WithoutTransaction leaving the earlier statement committed, query rejected
      as UnsupportedError, both missing DSN settings explained by name, and
      connection release
    external_package: >
      both suites are package foo_test rather than package foo. For postgres that
      is also the only vantage point proving the pgx types stay nameable on the
      vendored backend
  real_tinygo:
    postgres: >
      examples/pgxdemo under tinygo 0.41.1 prints "sqlbatch total: 42" from a
      mixed batch, alongside the raw pgx batch and the struct scan, and
      cancellation still lands at 620ms
    mysql: >
      a probe main under tinygo inserted three rows in one batch and read back
      affected=1 with ids 1, 2, 3, proving the structural mysql.Result assertion
      holds against the fork
  not_measured: >
    no round-trip or latency numbers yet. The requirement still rests on
    round-trip count alone, per the measurement note that was here before
prior_evidence: >
  requirement:postgres-driver-validation already records pgx Batch working under
  tinygo with tls stubbed, and the memory of the mysql validation records
  multiStatements working under tinygo. Neither was exercised through a public
  api or over tls
```
