---
id: decision:batch-transport-per-driver
type: decision
title: One Batch API, a Different Transport per Driver
---
api:sql-batch keeps one caller-facing shape while each driver package supplies the transport its protocol actually offers: pipelining on postgres, multiStatements on mysql.

```yaml
state: accepted, implemented 2026-08-03
selection:
  postgres:
    transport: pgx Conn.SendBatch, extended protocol pipelining
    reached_by: api:pgx-raw-conn, which already solves the type-naming problem
    tag_shape: registered in a build-tagged file per rule:build-tag-selection, because
      the *stdlib.Conn type is upstream pgx on host go and the fork on tinygo
  mysql:
    transport: statements joined with ";" in one comQuery, multiStatements=true
    reached_by: >
      driver.ExecerContext plus a structural assertion to
      interface{ AllRowsAffected() []int64; AllLastInsertIds() []int64 }
    no_import_needed: >
      that interface is satisfied by both upstream mysql.Result and the fork's
      Result, so the adapter compiles against database/sql/driver alone and needs
      no build tag of its own
    constrained_by: rule:mysql-multi-statement-batch
  sqlite: sequential execution on the leased conn, api parity only, phase p3
mysql_transaction_cost:
  problem: >
    the contract needs atomicity, and multiStatements has none. Opening the
    transaction in its own round trip would eat most of what batching buys
  shape: >
    START TRANSACTION rides along at the head of the joined statement and COMMIT
    is appended, so a successful batch still costs one round trip. A failed one
    costs a second, because the server stops at the failure and never reaches the
    queued COMMIT, leaving the transaction open on a connection about to return
    to the pool. The adapter sends ROLLBACK itself
  result_alignment: >
    the two control statements produce their own entries in AllRowsAffected, so
    the adapter trims one from each end and then checks the count against what
    was queued. A short count means the server stopped without reporting an
    error, which would otherwise read as success
why_not_one_transport:
  measured_absence: >
    system:go-sql-driver-mysql exposes no way to send a second command before the
    first reply is read, and neither MySQL nor the driver implements
    COM_STMT_BULK_EXECUTE. multiStatements is the only amortization available
  asymmetry_is_the_point: >
    the shared layer owns queueing, ordering, result delivery and error
    attribution. The wire shape is not shared because it cannot be
rows_layer:
  postgres_is_unaffected: >
    pgx already returns rows per queued query, so postgres ships Exec and Query
    together in p1. Only the mysql side has a rows problem
  problem: >
    inside sql.Conn.Raw the mysql adapter has only driver.Rows, whose Next fills
    []driver.Value, and database/sql keeps convertAssign unexported. A portable
    Scan would mean reimplementing it: the pointer, sql.Scanner and time.Time cases
  route_a_own_scan:
    what: write the conversion layer and keep everything inside one Raw callback
    gains: exec and query results in one batch, per-statement affected rows intact
    costs: a conversion layer to write and to keep correct against database/sql
  route_b_sql_rows:
    what: >
      send the joined statement through db.QueryContext instead of Raw, and walk
      the results with sql.Rows.NextResultSet. mysqlRows implements
      driver.RowsNextResultSet, so database/sql exposes every result set
    gains: real *sql.Rows with real Scan, no conversion layer at all
    costs: >
      sql.Result hides AllRowsAffected, and sql.Rows exposes no affected count, so
      a batch mixing writes and reads loses per-statement exec results
    shape: >
      the two Rows types differ only in signature, pgx Close() against sql Close()
      error and Columns; a wrapper each way needs no value conversion
  undecided: >
    open as mysql_query_route in requirement:sql-batch-execution. Route B likely
    wins if mixed batches turn out to be rare
rejected:
  pipeline_the_mysql_fork:
    what: write several comStmtExecute packets before reading, inside tinygomysql
    why_not: >
      the fork only exists on tinygo. Host go uses upstream, so batching would
      behave differently per compiler, which contradicts the identical-api rule in
      rule:build-tag-selection and doubles the test matrix
    also: MySQL does not document pipelining, and proxies may reorder or stall
  wrap_the_driver_conn:
    what: register our own driver name wrapping the upstream connector, adding SendBatch
    why_not: >
      a driver.Conn wrapper must forward SessionResetter, Validator,
      NamedValueChecker, Pinger and the context variants; one missed forward
      silently disables pooling or cancellation
  type_name_sniffing:
    what: match fmt.Sprintf("%T", dc) against the known driver conn types
    why_not: breaks on any vendoring or rename, and fails silently
  exec_the_joined_sql_through_sql_db:
    what: db.ExecContext with a ";"-joined statement, no Raw at all
    why_not: >
      sql.Result hides AllRowsAffected, so per-statement results are lost. This is
      the entire reason the mysql path needs Raw
  closure_shaped_api:
    what: Batch(func(h) error, func(h) error), each closure reading like ordinary code
    why_not:
      - a batch must know every statement before the first result returns, so a
        closure that branches on what it just read cannot be batched at all. The
        handle can only queue, never execute
      - so the closure body must not scan, which makes imperative-looking code
        that silently does not run in order. The pgx shape says deferred out loud
      - the error a closure returns cannot describe its query, since no query has
        run when it returns
    residue: >
      the safe subset, a handle that registers destinations, is exactly
      QueuedQuery.QueryRow(fn) with more ceremony and no pgx compatibility
  union_all_rewriting:
    what: >
      merge the queued SELECTs into one statement with UNION ALL, add a
      discriminator column, and split the rows back out per query on read
    round_trips: one, the same as multiStatements, so there is nothing to gain
    why_not:
      - reads only; INSERT, UPDATE and DELETE cannot appear in a union, and the
        write side is what motivates batching at all
      - every branch must have the same arity, so shorter queries need NULL padding
      - mysql aggregates the column type across branches, so an INT in one branch
        beside a VARCHAR in another comes back as text. A library cannot convert
        back, because it never knew the per-branch types
      - column names come from the first branch only
      - branch order is not guaranteed without ORDER BY on the discriminator, and
        that materializes the whole union and ends streaming
      - one error kills the statement with worse attribution than multiStatements
      - a distinct batch shape is a distinct statement digest, so nothing is reused
    narrow_case: >
      when multiStatements cannot be enabled at all, this is the only way to get
      N reads in one round trip through plain db.QueryContext. That is caller-side
      query rewriting, not a transport, and stays out of requirement:sql-batch-execution
    write_side_analogue: >
      multi-row INSERT ... VALUES (...),(...) is the same idea and is strictly
      better than joining N INSERTs, since it parses once. It also needs no
      library, which is why it is already listed out of scope
  postgres_simple_protocol_batch:
    what: force QueryExecModeSimpleProtocol so the batch is one string, as on mysql
    why_not: gives up server-side parameter binding and the statement cache for no gain
```
