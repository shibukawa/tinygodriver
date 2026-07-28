---
id: rule:pgx-vendoring
type: rule
title: pgx Vendoring and Update Duty
---
How the forked pgx sources live in the repository, following the pattern rule:mbedtls-vendoring already established for C sources.

```yaml
layout:
  path: database/sql/pgxstdlib/internal/pgx/
  contents: the whole pgx module, non-test sources only, import paths rewritten
  rewrite: github.com/jackc/pgx/v5 -> github.com/shibukawa/tinygodriver/database/sql/pgxstdlib/internal/pgx
why_whole_module:
  measured: >
    pgconn imports internal/pgio, internal/iobufpool and pgconn/internal/bgreader.
    Go rejects importing another module's internal: "use of internal package
    github.com/jackc/pgx/v5/internal/pgio not allowed"
  consequence: >
    a 3-file or single-package copy cannot compile; the copy must own every
    import path, which the rewrite accomplishes
  why_not_replace: >
    a replace directive only applies to the main module, so consumers of this
    library would silently get upstream pgx and fail to build under tinygo
version_pinning:
  current: v5.10.0
  recorded_in: vendor.py, alongside the expected checksum
  track: the v5 line
local_patches:
  file: PATCHES.md, one entry per change with its reason
  scope: pgconn/config.go, pgconn/pgconn.go, pgconn/auth_scram.go
  content:
    tls: crypto/tls and crypto/x509 removed, per requirement:no-crypto-tls-on-tinygo
    resolver: net.Resolver replaced, absent from tinygo's net
    startTLS: routed onto api:tls-upgrade
  rule: >
    every patch is recorded with its reason, so an upgrade can re-apply or drop
    it deliberately rather than by guesswork
update_duty:
  trigger: a pgx release carrying a fix this package needs, or a security advisory
  steps:
    - bump the version in vendor.py and re-run it
    - re-apply the recorded patches and reconcile any that no longer apply
    - run the test matrix on both compilers against a real postgres
  note: >
    the patch surface is deliberately small so this stays cheap; keep it that way
license:
  pgx: MIT
  attribution: keep the upstream LICENSE file inside the vendored directory
