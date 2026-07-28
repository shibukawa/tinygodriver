---
id: requirement:tinygo-postgres-driver
type: requirement
title: TinyGo-Capable PostgreSQL Driver
---
A `database/sql` PostgreSQL driver that compiles and runs under both TinyGo and standard Go, so application and test code is shared across compilers.

```yaml
priority: must
package: database/sql/pgxstdlib
import: github.com/shibukawa/tinygodriver/database/sql/pgxstdlib
naming: >
  named after the backend rather than the database, because the package is
  pgx/stdlib on both paths and does not implement postgres itself
implementation: decision:postgres-backend-split
surface:
  shape: database/sql only, mirroring database/sql/sqlite
  entry: Open returning *sql.DB
  native_escape_hatch: >
    sql.Conn.Raw yields *stdlib.Conn and then *pgx.Conn, so Batch, CopyFrom and
    LISTEN/NOTIFY stay reachable without widening the public api. Verified under
    tinygo
must:
  - identical public api under every build tag combination
  - accept a libpq-style url or keyword dsn
  - scram-sha-256 and md5 auth, both verified working under tinygo
  - honor context cancellation per rule:postgres-query-cancellation
  - build under -scheduler=threads per rule:tinygo-threads-scheduler
should:
  - install the CancelRequest watcher by default, not leave it to the caller
inherited_from_pgx: >
  type coverage, prepared statements, transactions, column metadata, sqlstate
  errors, Batch, CopyFrom and LISTEN/NOTIFY come from system:pgx on both paths
  and are not reimplemented
out_of_scope:
  - unix domain sockets and ipv6, blocked by system:tinygo-netdev
  - -scheduler=tasks, see rule:tinygo-threads-scheduler
  - the tls backend itself; this package only consumes api:tls-upgrade
phases:
  p1:
    what: package skeleton plus the vendored fork, sslmode=disable
    state: done 2026-07-28
  p2:
    what: wire the CancelRequest watcher through stdlib.OpenDB
    state: done, folded into p1; both backends install it by default
  p3:
    what: supply a DialFunc returning an fd-carrying conn
    state: done 2026-07-28
  p4:
    what: route pgconn startTLS onto api:tls-upgrade, restoring sslmode
    state: done 2026-07-28
verified_p1:
  both_backends: >
    the same suite passes against postgres 17 on upstream pgx and on the
    vendored fork via force_tinygo_logic
  real_tinygo: >
    examples/pgxdemo connects, queries and cancels under tinygo; cancellation
    lands in 610ms against a 3s query with SQLSTATE 57014
  covers: >
    ping, scalar and NULL scanning, parameters, sql.ErrNoRows, transactions with
    rollback, prepared statements, ColumnTypes, cancellation, 20 concurrent
    queries through the pool
verified_p4:
  tls_matrix: >
    verify-full with a custom sslrootcert, require, disable staying plaintext,
    rejection of an unknown CA, and rejection of a certificate issued for
    another host; each asserted against pg_stat_ssl for the live session
  cancellation_over_tls: 617ms against a 3s query, on the vendored backend
  real_tinygo: >
    examples/pgxdemo reports encrypted=true TLSv1.2 under Secure Transport and
    TLSv1.3 under -tags darwinstarttlswith13, both with working cancellation
  fixture_note: >
    a self-signed CA:TRUE certificate used directly as a server leaf is accepted
    by crypto/tls but rejected by Secure Transport; the tests use a real
    CA-to-leaf chain with serverAuth, which is what production looks like
supersedes: >
  the database/sql/sqlite README statement that postgres has no reliable tinygo
  path; requirement:postgres-driver-validation measures otherwise
