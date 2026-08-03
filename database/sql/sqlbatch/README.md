# sqlbatch

Send several SQL statements per network round trip through a `database/sql`
handle.

`database/sql` has no batch verb, so N statements normally cost N round trips.
That is worst under TinyGo, where each round trip is a blocking native socket
read. This package queues the statements, hands them to whichever transport the
driver actually offers, and delivers the results in queue order.

```go
var n int
b := &sqlbatch.Batch{}
b.Queue("INSERT INTO t(a) VALUES ($1)", 1)
b.Queue("SELECT count(*) FROM t").QueryRow(func(r sqlbatch.Row) error {
	return r.Scan(&n)
})
err := sqlbatch.Send(ctx, db, b)
```

Results arrive through callbacks registered on the queued statement. That is
not decoration: the raw driver connection is only valid inside
`sql.Conn.Raw`, so anything holding it has to be finished before the batch
returns.

Placeholders stay the driver's own — `$1` for PostgreSQL, `?` for MySQL. This
package does not translate between them.

## Semantics

A batch stops at the first failing statement, and nothing it did survives.
Later statements do not run, even independent ones.

That is PostgreSQL's native pipeline behaviour, and the other adapters
reproduce it with an explicit transaction. `WithoutTransaction()` gives it up
deliberately, in exchange for one less round trip where a transaction had to be
added.

How many round trips a batch costs depends on the driver and is allowed to
differ. What a caller observes — results and errors — is not.

## Driver support

| Driver | Package | Exec | Query | Cost |
| --- | --- | --- | --- | --- |
| PostgreSQL | `pgxstdlib` | yes | yes | one round trip, pipelined |
| MySQL / MariaDB | `mysql` | yes | with `WithFallback()` | one round trip, `multiStatements` |
| SQLite | `sqlite` | yes | yes | one statement at a time, one transaction |

A driver package registers its own adapter, so importing the package you
already open the database with is enough.

SQLite is not a lesser case, just a different cost. There is no network, so
there are no round trips to save; what costs is the fsync each autocommit
statement pays, and one transaction removes it. Measured on all three SQLite
backends, 200 inserts go from ~50ms to ~1ms. Running the statements separately
also means SQLite reports per-statement `RowsAffected` and `LastInsertId`, and
names the exact failing statement — both better than the MySQL path manages.

MySQL needs `multiStatements=true` in the DSN, and `interpolateParams=true`
whenever a statement carries arguments. Both are negotiated when the connection
is made, so a batch cannot turn them on.

## Unsupported drivers

`Send` never falls back to running the statements one at a time. A driver that
cannot serve the batch makes it fail.

The alternative is worse. A silent fallback would report success while costing
N round trips instead of one, and would quietly drop the all-or-nothing
guarantee that a batch on a working driver provides. Refusing keeps the cost of
a batch something you can reason about.

Every refusal is an `*UnsupportedError` naming the driver, what was missing,
and where possible what would fix it:

- **No adapter registered** — any third-party driver. `Capability` is
  `"batch"`.
- **A capability the adapter lacks** — a queued `Query` on MySQL. The batch is
  refused as a whole rather than running the statements it could.
- **A DSN the adapter cannot work with** — MySQL without `interpolateParams`.
  `Hint` names the setting to change.

In all of these the batch executes nothing: the refusal happens before any
statement reaches the server, so there is no partial effect to undo.

`WithFallback()` turns a refusal into sequential execution instead, for
portable code that would rather be slow on one database than not run there:

```go
err := sqlbatch.Send(ctx, db, b, sqlbatch.WithFallback())
```

It is opt-in because the cost changes from one round trip to one per statement,
and a caller who batched for speed should not get that silently. The semantics
do not change: same order, same stop at the first error, same rollback.

Or detect it and decide yourself:

```go
var unsupported *sqlbatch.UnsupportedError
if errors.As(sqlbatch.Send(ctx, db, b), &unsupported) {
	// ...
}
```

## What a batch does not promise

Reads in one batch do not necessarily see the same snapshot. PostgreSQL takes a
fresh one per statement at READ COMMITTED, while SQLite and MySQL InnoDB share
one across the transaction the batch runs in. Nothing here can reconcile that,
so a batch promises only that the statements run in order, stop at the first
error, and roll back together.

## Errors

A failure carries a `*StatementError` saying which queued statement it was:

```go
var se *sqlbatch.StatementError
if errors.As(err, &se) {
	log.Printf("statement %d failed: %v", se.Index, se.Err)
}
```

`Index` is `-1` when the position is unknown, which is not the same as
statement 0. PostgreSQL always knows; MySQL usually does not, because the
server reports one error for the whole batch.

## Adding an adapter

A driver package registers in `init`, keyed on its own driver type:

```go
func init() { sqlbatch.Register(&MyDriver{}, sendBatch) }

func sendBatch(ctx context.Context, dc any, b *sqlbatch.Batch, o sqlbatch.Options) (sqlbatch.Results, error)
```

A driver with no batch transport, where running the statements one at a time is
the right answer anyway, registers for the sequential path instead and needs no
adapter at all:

```go
func init() { sqlbatch.RegisterSequential(&MyDriver{}) }
```

An adapter that cannot serve a batch must return `*UnsupportedError` **without
having executed anything**. `WithFallback()` retries sequentially on refusal,
and that is only safe while this holds.

`dc` is what `sql.Conn.Raw` yields. An adapter reaching it directly bypasses
`database/sql`'s own argument conversion, so use `sqlbatch.ConvertArgs`, which
honours the driver's `NamedValueChecker` first.
