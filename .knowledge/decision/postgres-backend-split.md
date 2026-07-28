---
id: decision:postgres-backend-split
type: decision
title: Upstream pgx/stdlib on Host Go, Vendored Fork on TinyGo
---
Both paths are `pgx/stdlib` behind one `database/sql` surface; a build tag decides whether the import resolves to upstream pgx or to the vendored fork.

```yaml
state: accepted
selection:
  std_go: github.com/jackc/pgx/v5/stdlib, unmodified
  tinygo: the vendored fork, see rule:pgx-vendoring
  tags: rule:build-tag-selection, force_tinygo_logic exercises the fork under host go
  mechanism: >
    two small files differing only in which stdlib they import; the public
    Open signature is identical, so no caller sees the split
why_stdlib_not_native_pgx:
  measured: >
    pgx/stdlib pulls in the entire pgx module anyway, so choosing it costs no
    extra vendoring: pgx, pgconn, pgproto3, pgtype, pgxpool, ctxwatch and four
    internal packages, plus pgxpool over the native set
  gains:
    - matches database/sql/sqlite, so application code is shared
    - one public surface on both compilers
  keeps: >
    stdlib.OpenDB takes a pgx.ConnConfig, so rule:postgres-query-cancellation is
    still configurable, and sql.Conn.Raw still reaches *pgx.Conn
  costs: 0.27 MB of binary over calling pgx directly, measured under tinygo
why_a_fork_at_all:
  compile: >
    pgconn is the only package touching crypto/tls, crypto/x509 and net.Resolver,
    but requirement:no-crypto-tls-on-tinygo makes those unbuildable under tinygo
  no_partial_copy: >
    pgconn imports pgx internal packages, and Go forbids importing another
    module's internal, so the copy has to be whole-module. See rule:pgx-vendoring
  no_replace: >
    a replace directive is ignored in a dependency, so it cannot help a library
rejected:
  own_wire_driver:
    what: hand-written driver speaking system:postgres-wire-protocol
    why_not: reimplements pgproto3 and pgtype, 106 files, to avoid a 3-file patch
  patch_lib_pq:
    what: vendor lib/pq instead
    why_not: maintenance mode, and a smaller type layer
  pgx_unmodified_on_tinygo:
    what: keep upstream pgx on both paths
    why_not: crypto/tls is a tinygo stub, so pgconn cannot compile at all
divergence_to_document:
  - the tinygo path has no client certificates until api:tls-upgrade carries them
  - tls availability differs per platform, see decision:postgres-tls-via-proxy
