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

| Driver | Package | Exec | Query |
| --- | --- | --- | --- |
| PostgreSQL | `pgxstdlib` | yes | yes |
| MySQL / MariaDB | `mysql` | yes | no |
| SQLite | `sqlite` | no | no |

A driver package registers its own adapter, so importing the package you
already open the database with is enough.

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

- **No adapter registered** — today SQLite and any third-party driver.
  `Capability` is `"batch"`. SQLite would gain nothing anyway: a local file has
  no round trip to save.
- **A capability the adapter lacks** — a queued `Query` on MySQL. The batch is
  refused as a whole rather than running the statements it could.
- **A DSN the adapter cannot work with** — MySQL without `interpolateParams`.
  `Hint` names the setting to change.

In all of these the batch executes nothing: the refusal happens before any
statement reaches the server, so there is no partial effect to undo.

```go
err := sqlbatch.Send(ctx, db, b)

var unsupported *sqlbatch.UnsupportedError
if errors.As(err, &unsupported) {
	// Run them one at a time instead, accepting the round trips.
}
```

Writing that fallback by hand is deliberate. Only the caller knows whether N
round trips are acceptable, and whether the statements need a transaction
around them once the batch is no longer providing one.

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

`dc` is what `sql.Conn.Raw` yields. An adapter reaching it directly bypasses
`database/sql`'s own argument conversion, so use `sqlbatch.ConvertArgs`, which
honours the driver's `NamedValueChecker` first.
