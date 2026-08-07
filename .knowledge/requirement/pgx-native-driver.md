---
id: requirement:pgx-native-driver
type: requirement
title: pgx-Native PostgreSQL Driver Surface
---
A public `database/pgx` package exposing the pgx-native API on both compilers, because the `database/sql` surface of requirement:tinygo-postgres-driver costs performance and serializes under load; pgxstdlib then becomes the `database/sql`-compatible layer implemented on top of the same core, mirroring upstream's pgx-to-stdlib layering.

```yaml
priority: must
requested: 2026-08-07
state: >
  shipped 2026-08-07 as api:pgx-native; verification lives there. Later the
  same day the maintainer mirrored upstream layout fully: pgxstdlib became
  database/pgx/stdlib with no compatibility alias, and database/pgx/pgxpool
  shipped, closing requirement:pgxpool-tinygo
package: database/pgx
import: github.com/shibukawa/tinygodriver/database/pgx
package_name: pgx, so call sites read as upstream pgx code
why_not_enough_pgxstdlib:
  locking: >
    database/sql guards every driver conn with sql.DB's pool mutex and a
    per-conn mutex held across each call, so concurrent load serializes on
    lock acquisition before pgx ever sees the query
  conversion: >
    parameters and results funnel through driver.Value, one interface boxing
    per value each way, discarding pgx's direct pgtype binary codec path
  escape_hatch_shape: >
    api:pgx-raw-conn reaches Batch, CopyFrom and LISTEN/NOTIFY, but only
    inside a callback whose lease forbids anything derived from the conn to
    outlive it; long-lived native use, a held Rows, or a resident LISTEN
    conn do not fit that shape
surface:
  entry: ParseConfig, Connect, ConnectConfig returning *pgx.Conn
  conn: Query, QueryRow, Exec, SendBatch, CopyFrom, Prepare, Begin/Tx,
    WaitForNotification, Close
  values: the pgx row-collection helpers and CollectableRow set already
    enumerated in api:pgx-raw-conn, plus ConnConfig and pgconn config types
    so a caller can set DialFunc or tracing without importing internals
  mechanism: >
    the alias-per-backend pattern proven by api:pgx-raw-conn, widened from an
    escape hatch to the whole package: host go aliases upstream
    github.com/jackc/pgx/v5 so type identity matches third-party code;
    tinygo aliases the vendored fork; generic functions stay one-line
    forwards per the same api's generic_forwards note
  defaults: >
    Connect wires the same defaults pgxstdlib.open sets today: the
    CancelRequest watcher per rule:postgres-query-cancellation and, on the
    tinygo path, the fd-carrying DialFunc that lets sslmode ride
    api:tls-upgrade
layering:
  direction: >
    database/pgx becomes the base; pgxstdlib keeps its public Open api
    unchanged and is reimplemented as the database/sql adapter over the same
    core, which is exactly how upstream pgx and pgx/stdlib relate
  fork_location: >
    the vendored fork must move out of pgxstdlib/internal to a directory
    whose internal scope covers both packages; database/internal/pgx is the
    candidate, since Go's internal rule then admits every package under
    database/ and nothing else. rule:pgx-vendoring's layout entry changes
    when this lands
  compat: >
    no pgxstdlib caller sees the move; its tests and examples/pgxdemo keep
    passing unchanged as the regression guard
must:
  - identical public api under every build tag combination
  - on host go, database/pgx types are upstream pgx types, compile-time
    asserted as compat_std_test.go does for the aliases today
  - no database/sql import anywhere on the query path
  - accept the same libpq-style url and keyword dsn as pgxstdlib
  - sslmode semantics identical to pgxstdlib Open, including the verify-ca
    promotion and the sslcert rejection
  - honor context cancellation per rule:postgres-query-cancellation
  - build under -scheduler=threads per rule:tinygo-threads-scheduler
  - pgxstdlib public api and behavior unchanged after relayering
should:
  - a benchmark against pgxstdlib on the same workload, covering single-conn
    query latency and allocations, and concurrent throughput across conns,
    so the locking claim is measured rather than asserted
out_of_scope:
  - pgxpool, tracked as requirement:pgxpool-tinygo; database/pgx/pgxpool is
    its natural landing spot once the core surface exists
  - unix domain sockets and ipv6, same netdev limits as pgxstdlib
  - the tls backend itself; this package only consumes api:tls-upgrade
verification:
  suites: >
    the pgxstdlib matrix re-expressed against the native surface, both
    backends via force_tinygo_logic against a real postgres: connect, scalar
    and NULL scans, parameters, tx with rollback, prepared statements,
    Batch, CopyFrom, LISTEN/NOTIFY, cancellation timing, tls matrix
  boundary: >
    an example outside database/pgx built with real tinygo, because only an
    external caller proves the internal-import rule holds, the same lesson
    was_broken_on_tinygo records in requirement:tinygo-postgres-driver
  relayering: the unchanged pgxstdlib suite is the proof the adapter rewrite
    changed nothing observable
```
