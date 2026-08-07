---
id: api:pgx-native
type: api
title: database/pgx Native Surface
---
`database/pgx` re-exports the pgx-native API on both compilers and installs the two defaults every connection needs, satisfying requirement:pgx-native-driver; pgxstdlib layers its `database/sql` adapter on the config this package parses.

```yaml
type: |
  func Connect(ctx context.Context, dsn string) (*Conn, error)
  func ConnectConfig(ctx context.Context, cfg *ConnConfig) (*Conn, error)
  func ParseConfig(dsn string) (*ConnConfig, error)
aliases:
  types: Conn, ConnConfig, Tx, TxOptions, TxIsoLevel, TxAccessMode,
    TxDeferrableMode, Batch, BatchResults, QueuedQuery, Rows, Row, RowScanner,
    Identifier, CopyFromSource, LargeObjects, NamedArgs, StrictNamedArgs,
    QueryExecMode, QueryTracer, CommandTag, Notification, PgError, PgConn,
    FieldDescription, CollectableRow, RowToFunc
  consts: the TxIsoLevel/TxAccessMode/TxDeferrableMode values and the five
    QueryExecMode values, forwarded as typed constants
  vars: ErrNoRows, ErrTooManyRows, ErrTxClosed, ErrTxCommitRollback,
    CopyFromRows, CopyFromSlice, CopyFromFunc, RowToMap, ForEachRow
  generic_forwards: same set api:pgx-raw-conn records, kept in step by hand
mechanism: >
  the per-backend alias pattern of api:pgx-raw-conn widened to a whole
  package: backend_std.go binds upstream github.com/jackc/pgx/v5,
  backend_native.go the vendored fork under database/internal/pgx per
  rule:pgx-vendoring. compat_std_test.go asserts upstream type identity and
  sentinel-error identity at compile time on the std build
defaults_installed_by_parseconfig:
  watcher: BuildContextWatcherHandler per rule:postgres-query-cancellation,
    both backends
  dialer: the fd-carrying https.DialPlain on the native backend only, which is
    what lets sslmode ride api:tls-upgrade
  overridable: both are plain fields on the returned ConnConfig
layering: >
  the family mirrors upstream pgx since the 2026-08-07 rename: this package
  is the base, database/pgx/pgxpool the pool, database/pgx/stdlib the
  database/sql adapter (formerly database/sql/pgxstdlib, no compatibility
  alias). stdlib.open is ParseConfig here plus the per-build pgx/stdlib
  OpenDB, and stdlib names pgx types through this package instead of
  re-exporting its own
concurrency: >
  a Conn is single-owner, unlike a sql.DB handle; one conn per goroutine, or
  database/pgx/pgxpool for a shared pool, see requirement:pgxpool-tinygo
limits:
  pgtype: registering custom codecs needs the pgtype package itself, which
    stays internal on the tinygo build; std-go-only for now
  copyfrom_over_tls: >
    the native TLS conns serialize read and write on one session lock, so
    CopyFrom over sslmode!=disable on the tinygo path can deadlock the same
    way the plaintext path did before the fix recorded in api:tls-upgrade;
    plaintext CopyFrom is fixed and verified
verified:
  suites: >
    15 cases on both backends against postgres 17 on darwin and linux
    (container): connect, scalars and NULL, params, ErrNoRows, tx with
    rollback and ErrTxClosed, named prepared statements, Batch, CopyFrom,
    LISTEN/NOTIFY, RowToStructByName collection, FieldDescriptions, PgError
    SQLSTATE, cancellation at ~610ms, 8 concurrent conns, ParseConfig defaults
  real_tinygo: >
    examples/pgxnativedemo with tinygo 0.41.1 -scheduler=threads: batch,
    CopyFrom, struct collection and cancellation all pass; the example lives
    outside database/ so its build proves the internal-import boundary
  benchmark: >
    native vs pgxstdlib on the same SELECT: 6 vs 15 allocs, 352 vs 754 B/op;
    ns/op is RTT-bound on localhost so the win is boxing, not wall time
  known_issue: >
    sustained query loops under real tinygo -scheduler=threads intermittently
    nil-deref in the runtime, pre-existing and equally present in pgxstdlib;
    -scheduler=tasks does not crash. Under investigation separately
```
