# `database/sql` driver over the Spin PostgreSQL host interface

Status: proposed, 2026-08-19. Verified against Spin 4.0.2, TinyGo 0.41.1, wasmtime 47.0.2.

## Why this exists

A wasip2 component cannot open a socket. TinyGo's `net` package routes every
connection through a netdev, and `netdev/sys_wasi.go` stubs all of them:
`wasip1` has no outbound sockets at all, and the `wasip2` wasi-sockets backend is
not implemented. A `postgres://` build therefore compiles and dies at the first
connection.

Spin closes that hole from the host side rather than the guest side, and says so
in its own documentation, for the same reason recorded here:

> The current version of the WebAssembly System Interface (WASI) doesn't provide
> a sockets interface, so database libraries that depend on sockets can't be
> built to Wasm. The Spin interface means Wasm modules can bypass this limitation
> by asking Spin to make the database connection on their behalf.

So the work is not a wire-protocol implementation. It is an adapter: the host
already speaks PostgreSQL, and what is missing is the `database/sql` shape that
every consumer above expects.

## Scope

**In scope.** A `database/sql` driver for PostgreSQL backed by
`spin:postgres/postgres@4.2.0`, usable from a TinyGo `wasip2` component running
on a Spin host.

**Out of scope, with a reason.** MySQL. The interface shipped at Spin v4.0.2 is
`spin-mysql@3.0.0`, and it is async-only:

```wit
resource connection {
  open: static async func(address: string) -> result<connection, error>;
  query: async func(statement: string, params: list<parameter-value>)
      -> result<tuple<list<column>, stream<row>, future<result<_, error>>>, error>;
  execute: async func(statement: string, params: list<parameter-value>) -> result<_, error>;
}
```

`async func`, `stream<row>` and `future` are WASI 0.3-shaped. PostgreSQL is the
only one of the two that still offers a synchronous entry point, which is why it
is the whole of phase one. MySQL becomes possible when TinyGo can generate and
run bindings for the async ABI; that is a separate investigation, not a smaller
version of this one.

Also out of scope: `LISTEN`/`NOTIFY`, `COPY`, server-side cursors, multi-statement
strings, and the `connection-builder` resource's `set-ca-root`. None of them have
a `database/sql` surface, and adding them before the ordinary path works would
widen the test matrix for nothing.

## The host interface

From `wit/deps/spin-postgres@4.2.0/postgres.wit` at tag `v4.0.2`. Only the
synchronous half is in scope:

```wit
resource connection {
  open:    static func(address: string) -> result<connection, error>;
  query:   func(statement: string, params: list<parameter-value>) -> result<row-set, error>;
  execute: func(statement: string, params: list<parameter-value>) -> result<u64, error>;
}

record row-set {
  columns: list<column>,   // record column { name: string, data-type: db-data-type }
  rows:    list<row>,      // type row = list<db-value>
}
```

Three facts about that shape drive most of the design below.

`connection` is a **resource**, not a per-call address. State survives between
calls on the same handle, so `BEGIN` and `COMMIT` are ordinary statements rather
than something the interface has to offer.

There are **no prepared statements**. Every call carries the statement text and
its parameters together.

`query` returns a **fully materialised `row-set`**. There is no cursor and no
back-pressure; the host has already read every row before the guest sees the
first one.

## What `database/sql` requires

The driver implements the same surface the existing drivers in
`database/sql/sqlite` and `database/sql/mysql` do. Mapping each interface onto
the host:

| Go interface | Backed by | Notes |
| --- | --- | --- |
| `driver.Driver.Open` | `connection.open(address)` | The DSN is passed through unchanged; Spin parses it. |
| `driver.DriverContext` / `driver.Connector` | same | Preferred entry point, matching `tinygosqlite`. |
| `driver.QueryerContext` | `connection.query` | Avoids the prepare round trip that does not exist. |
| `driver.ExecerContext` | `connection.execute` | Returns `u64` affected rows. |
| `driver.Conn.Prepare` | stored statement text | Required by the interface; see below. |
| `driver.Tx` | `execute("BEGIN")` / `COMMIT` / `ROLLBACK` | `database/sql` pins one `Conn` per `Tx`, so the resource is the right unit. |
| `driver.Rows` | slice walk over `row-set.rows` | `Columns()` from `row-set.columns`. |
| `driver.Pinger` | `execute("SELECT 1")` or equivalent | The framework pings at startup. |

### Prepared statements that are not prepared

`driver.Conn` requires `Prepare`, and the host offers nothing to prepare against.
The statement type therefore stores the query text and delegates to
`query`/`execute` on use.

`NumInput()` must return `-1`. The driver cannot count placeholders without
parsing SQL, and `-1` is how a driver tells `database/sql` not to check the
argument count on its behalf. Returning a guess would reject valid calls.

Implementing `QueryerContext` and `ExecerContext` is what keeps the common path
off this shim entirely: `database/sql` uses them directly and never prepares.

### Transactions and savepoints

`BeginTx` issues `BEGIN`, with the isolation level appended when
`opts.Isolation` is not the default, and `READ ONLY` when `opts.ReadOnly` is set.
An isolation level the driver cannot express is an error rather than a silently
weaker transaction.

Savepoints are ordinary statements and need nothing special, which matters
because the consumer's nested-transaction handling is built on them.

### Context cancellation cannot work, and must say so

`connection.query` is a synchronous host call. Under the asyncify scheduler a
blocking call holds the runtime, so no goroutine runs to observe a deadline, and
there is no way to abort a call already in flight.

The driver must check `ctx.Err()` **before** issuing each call and return it, and
must not pretend to do more. This is the same failure the consumer already
guards against: `popcornweb/database/postgres/scheduler_tinygo.go` refuses to
build under the cooperative scheduler precisely because a query outlived its
deadline and returned a nil error with nothing logged. Documenting the limit is
the requirement; hiding it would reproduce the bug that guard exists for.

A query that must be bounded has to be bounded server-side, with
`statement_timeout`.

### Pooling is mostly moot

`database/sql` pools `driver.Conn` values, and a Spin component is commonly
instantiated per request. A pooled connection then does not outlive the request
that opened it, so pool bounds tune something that mostly does not happen. The
driver should still honour `Close` correctly — dropping the resource handle — and
should not assume a long-lived process.

## Type mapping

`driver.Value` admits only `int64`, `float64`, `bool`, `[]byte`, `string`,
`time.Time`, and `nil`. The host's `db-value` variant is much wider, so the
mapping is where this driver earns or loses its correctness.

### Host to Go

| `db-value` | `driver.Value` | Notes |
| --- | --- | --- |
| `boolean` | `bool` | |
| `int8`, `int16`, `int32`, `int64` | `int64` | Widened; width is recoverable from `column.data-type`. |
| `floating32`, `floating64` | `float64` | |
| `str` | `string` | |
| `binary`, `jsonb` | `[]byte` | |
| `date(y,m,d)` | `time.Time` | UTC midnight. |
| `time(h,m,s,ns)` | `time.Time` | Zero date, UTC. |
| `datetime(y,m,d,h,mi,s,ns)` | `time.Time` | UTC; the interface states date-time is always UTC without zone. |
| `timestamp(s64)` | `time.Time` | Unix seconds. |
| `uuid` | `string` | |
| `decimal` | `string` | Base 10, lossless. Never `float64`. |
| `db-null` | `nil` | |
| `interval`, `range-*`, `array-*` | see below | No `driver.Value` shape. |
| `unsupported(list<u8>)` | `[]byte` | Raw wire bytes the host could not classify. |

`decimal` arriving as a string is deliberate upstream — the WIT comment reads
"I admit defeat. Base 10". Converting it to `float64` anywhere in this driver is
a defect, not a convenience.

The composite cases — `interval`, the three `range-*` and the four `array-*`
variants — have no scalar representation. **Decide once and record it:** either
render them to their PostgreSQL text form as `[]byte`, so a caller can `Scan`
into a string or a custom `sql.Scanner`, or return a typed error naming the
column. Silently dropping to `nil` is the one option that must not be taken.

### Go to host

The reverse mapping is the riskier direction, because `driver.Value` has already
erased the width by the time the driver sees it. An `int64` sent to an `int32`
column may come back as `bad-parameter` rather than being coerced.

The requirement is a **test matrix per column type**, round-tripping a value out
and back in, rather than a mapping written from the table above and assumed
correct. `nil` maps to `db-null`; `time.Time` needs a documented choice between
`datetime` and `timestamp`.

## Errors

The host returns a structured error worth preserving:

```wit
variant error {
  connection-failed(string),
  bad-parameter(string),
  query-failed(query-error),   // text(string) | db-error(db-error)
  value-conversion-failed(string),
  other(string),
}

record db-error {
  as-text: string, severity: string, code: string, message: string,
  detail: option<string>, extras: list<tuple<string, string>>,
}
```

Export a Go error type carrying `code`, `severity`, `message` and `detail`, with
`errors.As` support. `code` is the SQLSTATE, which is what any caller
distinguishing a unique violation from a deadlock needs; flattening the variant
to a string throws that away and cannot be added back later without changing the
error type.

`connection-failed` should map to `driver.ErrBadConn` where the connection is
unusable afterwards, so `database/sql` retires the handle instead of reusing it.

## Package shape

`database/sql/spinpg`, beside the existing `database/sql/{sqlite,mysql}`.

Build constraints: the package compiles only for `wasip2`, and every symbol
outside that build must fail with a reason rather than a link error — the
pattern `netdev/sys_wasi.go` already uses. A host build should still typecheck
the type-mapping functions, because that is where the tests are cheapest.

Bindings for `spin:postgres/postgres@4.2.0` are generated with
`wit-bindgen-go`, with `go.bytecodealliance.org/cm` as the runtime dependency.
The generated code is committed rather than generated at build time, matching how
TinyGo ships its own `internal/wasi` bindings.

## Cross-repo dependency

This driver cannot be reached unless the component's WIT world imports the
interface. That world is owned by Popcorn Web, in
`internal/pwcli/wasihttp/proxy.wit`, and it currently imports only
`go:http/proxy` plus `wasi:cli/exit`.

Two changes therefore land outside this repository, and neither belongs here:

1. The generated world gains `import spin:postgres/postgres@4.2.0;` when the
   project declares both a Spin host and a PostgreSQL database.
2. A new engine package registers the `postgres://` scheme against this driver
   for that build. It must be a **separate package** from
   `popcornweb/database/postgres`, whose `scheduler_tinygo.go` guard refuses any
   cooperative-scheduler build — a component is always cooperative, so linking
   that package would fail the build for a reason that no longer applies.

The engine supplies `Open` only. `OpenNative` stays unset: it exists so the
request path can bypass `database/sql` for a pgxpool, and there is no pool here
to bypass it for.

## Testing

Three layers, in the order they pay off.

**Type mapping, host build, no wasm.** Pure functions over the variant types,
tested with ordinary `go test`. Most of the defects this driver can have live
here, and this is the only layer that costs nothing to run.

**A live round trip under Spin.** `spin up` with a real PostgreSQL and
`allowed_outbound_hosts` naming it, exercising the column-type matrix in both
directions. This is the layer that catches the parameter-width question above,
and it cannot be faked: the whole point is that the host does the conversion.

**The consumer's suite.** The Popcorn Web todo example against this driver, which
is where transactions, savepoints and the framework's own SQL land.

## Acceptance

- `sql.Open` through this driver serves `SELECT`, `INSERT`, `UPDATE`, `DELETE`
  and transactions from a TinyGo `wasip2` component on Spin 4.0.2.
- Every `db-value` variant has a decided mapping, including the composite ones,
  and a test that asserts it rather than a comment that describes it.
- A SQLSTATE survives to the caller through `errors.As`.
- The absence of context cancellation is documented at the package doc comment,
  not only here.
- The package does not build for a non-`wasip2` target, and says why.
