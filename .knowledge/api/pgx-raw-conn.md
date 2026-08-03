---
id: api:pgx-raw-conn
type: api
title: pgx Connection Access
---
`WithConn` hands the pgx connection behind a `database/sql` handle to a callback, and `pgxstdlib` re-exports the pgx types so the callback can name what it receives on both compilers.

```yaml
type: |
  func WithConn(ctx context.Context, db *sql.DB, fn func(*Conn) error) error
  func WithSQLConn(sc *sql.Conn, fn func(*Conn) error) error
aliases:
  types: Conn, Batch, BatchResults, QueuedQuery, Rows, Row, Identifier,
    CopyFromSource, CollectableRow, RowToFunc, CommandTag, Notification, PgError
  funcs: CopyFromRows, CopyFromSlice, RowToMap, ForEachRow, as vars since Go has
    no function alias
  generic_forwards:
    what: AppendRows, CollectRows, CollectOneRow, CollectExactlyOneRow, RowTo,
      RowToAddrOf, and the four RowToStructBy* pairs
    why_not_aliases: >
      a generic function can be neither aliased nor bound to a var: "cannot use
      generic function without instantiation". They are one-line forwards
    cost: the only names that must be kept in step by hand across a pgx upgrade
  bound_in: backend_std.go and backend_native.go, same names against a different pgx
  why: >
    the tinygo build's pgx sits under internal/, so a caller outside pgxstdlib
    cannot import it. An alias may point at an internal type, which is what makes
    the value usable. See was_broken_on_tinygo in requirement:tinygo-postgres-driver
compatibility:
  host_go: >
    the aliases are the upstream pgx types themselves, asserted at compile time
    in compat_std_test.go. A *pgxstdlib.Conn is a *pgx.Conn, so code passes
    either way with no conversion and no method loss
  tinygo: >
    same api, different identity. The vendored copy is v5.10.0 under a different
    import path, so its types are not upstream's
  what_that_costs: >
    own code written against the pgxstdlib names is portable across both
    compilers. A third-party package whose own signatures name *pgx.Conn is host
    go only, and no alias can fix that
  guard: >
    compat_std_test.go carries "!tinygo && !force_tinygo_logic" and is pure
    compile-time assertions. Turning any alias into a wrapper type would compile
    everywhere else and only break in a caller
serves:
  - pgx Batch, the shape adopted by api:sql-batch
  - CopyFrom
  - LISTEN/NOTIFY through Conn.Exec and Conn.WaitForNotification
  - errors.As against *PgError for SQLSTATE, also unreachable before
lifetime:
  rule: nothing derived from c may outlive fn, including BatchResults and Rows
  why: >
    WithConn is built on sql.Conn.Raw, which holds the driver conn's mutex only
    for the callback. A read afterwards runs unlocked and can interleave with
    database/sql's own use of the same socket
  lease: WithConn leases and returns a pooled conn; WithSQLConn uses the caller's
dispatch: >
  asserts dc to interface{ Conn() *Conn } rather than to *stdlib.Conn, so
  rawconn.go carries no build tag; the alias in the method signature already
  resolved per build. A non-pgx driver gets a named error, never a panic
verified:
  suites: >
    seven cases on both backends against postgres 17, covering reach, batch,
    atomicity of a failed batch, connection release, session identity through
    WithSQLConn, the row-collection helpers, and rejection of a foreign driver
  tinygo: >
    examples/pgxdemo built with tinygo 0.41.1 and run against postgres 17; the
    batch returns 10, 20, 30, RowToStructByName fills a struct, and cancellation
    still lands at 610ms. The struct case matters most, since it is reflection
    over field names
  boundary: >
    the example is outside pgxstdlib/, so its build is what proves the internal
    import rule is satisfied. A test beside the package cannot
```
