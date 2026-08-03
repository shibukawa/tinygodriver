---
id: api:sql-batch
type: api
title: SQL Batch API
---
Public surface of `database/sql/sqlbatch`, the portable batch entry point for requirement:sql-batch-execution: a pgx-shaped `Batch` whose results arrive through callbacks, plus the registry each driver package fills.

```yaml
package: github.com/shibukawa/tinygodriver/database/sql/sqlbatch
caller_surface: |
  type Batch struct{ ... }
  func (b *Batch) Queue(sql string, args ...any) *QueuedQuery
  func (b *Batch) Len() int

  func (q *QueuedQuery) Exec(fn func(CommandTag) error) *QueuedQuery
  func (q *QueuedQuery) Query(fn func(Rows) error) *QueuedQuery
  func (q *QueuedQuery) QueryRow(fn func(Row) error) *QueuedQuery

  func Send(ctx context.Context, db *sql.DB, b *Batch, opts ...Option) error
  func WithoutTransaction() Option

  type CommandTag struct {
      RowsAffected    int64
      LastInsertID    int64
      HasLastInsertID bool
  }
adapter_surface: |
  type Adapter func(ctx context.Context, dc any, b *Batch, o Options) (Results, error)
  func Register(drv driver.Driver, a Adapter)
  type Options struct{ Transaction bool }
  type Results interface{ Exec() (CommandTag, error); Query() (Rows, error); QueryRow() Row; Close() error }
  func (b *Batch) Queued() []*QueuedQuery
  func (q *QueuedQuery) WantsRows() bool
  func ConvertArgs(dc any, args []any, ordinalBase int) ([]driver.NamedValue, error)
errors: |
  type UnsupportedError struct{ Driver, Capability, Hint string }
  type StatementError struct{ Index int; SQL string; Err error }
usage: |
  var n int
  b := &sqlbatch.Batch{}
  b.Queue("INSERT INTO t(a) VALUES ($1)", 1)
  b.Queue("SELECT count(*) FROM t").QueryRow(func(r sqlbatch.Row) error { return r.Scan(&n) })
  err := sqlbatch.Send(ctx, db, b)
design:
  callbacks_only: >
    Results is exported for adapters but never handed to a caller. Send drives
    it and runs the callbacks, so nothing derived from the connection can
    outlive the batch. See callback_scoped in requirement:sql-batch-execution
  registration: >
    a driver package registers in init, keyed on its driver's reflect.Type, so
    importing the package used to open the handle is all a caller does
  no_sql_conn_variant: >
    a Send taking a *sql.Conn the caller holds was dropped: adapters are keyed on
    the driver, database/sql exposes Driver() on *sql.DB but not on *sql.Conn, so
    such a call would have to take both and trust they match
  statement_error_index: >
    -1 means the position is unknown, never statement 0. Send attributes by
    counting results it read; an adapter that already knows better returns its
    own StatementError and Send leaves it alone
  convert_args: >
    exported because an adapter reaching driver.Conn directly bypasses
    database/sql's own conversion. It honours the driver's NamedValueChecker
    first, since a driver may accept types the default converter rejects
divergence_from_pgx:
  send_is_a_function: >
    pgx has Conn.SendBatch returning BatchResults for the caller to hold. Here
    the driver.Conn is valid only inside sql.Conn.Raw, so results are delivered
    to callbacks instead
  command_tag: >
    pgx returns pgconn.CommandTag, which has RowsAffected and no insert id.
    CommandTag adds LastInsertID with a validity flag, because
    system:go-sql-driver-mysql reports one per statement and postgres never does
  no_query_rewriter: pgx QueryRewriter and named args are pgx-internal and not portable
placeholders: >
  not normalized. postgres takes $1 and mysql takes ?, so portable caller code
  still needs its own sql per database; the api does not pretend otherwise
detail: flow:batch-send
```
