---
id: requirement:postgres-driver-validation
type: requirement
title: Existing PostgreSQL Driver Validation Result
---
Measured state of `lib/pq` and `pgx/v5` under TinyGo; this is the evidence base for decision:postgres-backend-split and rule:postgres-query-cancellation.

```yaml
priority: must
environment:
  tinygo: 0.41.1 darwin/arm64, llvm 20.1.1
  go: 1.26.5
  netdev: system:tinygo-netdev, in-repo worktree via replace
  server: postgres:17 in docker, scram-sha-256 auth
  drivers: github.com/lib/pq v1.12.3, github.com/jackc/pgx/v5 v5.10.0
compile_result:
  lib_pq:
    state: fails
    errors: 4, all in ssl.go
    symbols: tls.Config.Clone, tls.RenegotiateFreelyAsClient, net.TLSConn.ConnectionState, tls.X509KeyPair
  pgx:
    state: fails
    symbols: tls.Conn, tls.Config.Clone, tls.X509KeyPair, net.Resolver, net.DefaultResolver
  cause: requirement:no-crypto-tls-on-tinygo; sslmode=disable does not skip compilation of the tls files
  finding: >
    crypto/tls is the only compile blocker for lib/pq; pgx adds net.Resolver.
    With those stubbed, both packages compile clean under tinygo.
runtime_result_tls_stubbed:
  lib_pq: identical to host go on every case, 0 failures
  pgx: 1 failure, context cancellation, see rule:postgres-query-cancellation
  verified_working:
    - database/sql full surface: Query, Exec, Prepare, Tx, pool
    - scalar types int8, float8, text, bool, bytea, timestamptz, numeric, NULL
    - reflect paths: ColumnTypes, ScanType, pgx RowToStructByName and RowToStructByPos
    - scram-sha-256 auth over crypto/md5, sha256, hmac, pbkdf2
    - 20 concurrent queries through database/sql pool
    - LISTEN/NOTIFY via pq.Listener, COPY, pgx Batch and CopyFrom
    - pgtype numeric, uuid, interval, jsonb
    - errors.As to *pq.Error and *pgconn.PgError
  binary_size: host go 5.7 MB, tinygo 1.2 MB, same lib/pq program
patch_surface_measured:
  question: how much of pgx must change to build under tinygo
  method: grep the whole module for crypto/tls, crypto/x509, net.Resolver, net.LookupIP
  result: 3 non-test files, all in pgconn; pgtype, pgproto3, stdlib, pgx root clean
  cancellation: >
    0 files; Config.BuildContextWatcherHandler plus the public
    pgconn.CancelRequestContextWatcherHandler fixes it, measured 612ms and
    SQLSTATE 57014 under tinygo against 616ms on host go
  scheduler: >
    the CancelRequest handler still fails under -scheduler=tasks, 3.009s and
    err=nil, confirming rule:tinygo-threads-scheduler
conclusion: >
  the wire, type, auth, database/sql and reflect layers all work under tinygo.
  The remaining gap is 3 files of tls and resolver code plus one config setting,
  which is far cheaper than a hand-written driver; see
  decision:postgres-backend-split.
reproduce: >
  vendor the driver, replace the tls entry points with stubs, tinygo build, run
  against a local postgres with sslmode=disable
