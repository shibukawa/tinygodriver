# Local patches to the vendored pgx

Vendored by `vendor.py` from `github.com/jackc/pgx/v5 v5.10.0`.

Every change below is applied by `patch.py` after `vendor.py` runs. Keep this
file and that script in sync: on a version bump, re-run both and reconcile any
hunk that no longer applies. See `rule:pgx-vendoring`.

Upstream is otherwise untouched — 145 sources, import paths rewritten only.

## Why any patch is needed

`requirement:no-crypto-tls-on-tinygo` forbids `crypto/tls` and `crypto/x509` on
the TinyGo path, because TinyGo ships them as stubs that fail at link time
rather than at compile time. `pgconn` is the only package in pgx that touches
them, and it is also the only one that touches `net.Resolver`, which TinyGo's
`net` does not define at all.

Measured on the unpatched tree: three non-test files, all in `pgconn`.

## 1. `pgconn/config.go`

- Drop the `crypto/tls`, `crypto/x509` and `encoding/pem` imports.
- Replace `*tls.Config` with the local `TLSConfig` placeholder type, so the
  public `Config.TLSConfig` field keeps its shape without linking `crypto/tls`.
- Replace the body of `configTLS` so `sslmode=disable` succeeds and every other
  mode reports `ErrTLSUnsupported`. Never fall back to plaintext silently:
  `requirement:platform-matrix` forbids it.
- Replace `makeDefaultResolver`, which returned `net.DefaultResolver`, with a
  resolver that defers name resolution to the dialer. TinyGo has no
  `net.Resolver`, and netdev resolves names inside `Connect` anyway.

## 2. `pgconn/pgconn.go`

- Drop the `crypto/tls` import.
- `tls.Client` call sites return `ErrTLSUnsupported`.
- `startTLS` keeps its SSLRequest exchange untouched — that is protocol code,
  not TLS code — and only its final `tls.Client` line changes.

`startTLS` is the seam `api:tls-upgrade` plugs into in phase 4. Leaving its
message exchange intact is what makes that a one-line change later.

## 3. `pgconn/auth_scram.go`

- Drop the `crypto/tls` and `crypto/x509` imports.
- Channel binding requires a `*tls.Conn` to hash the peer certificate. With no
  TLS there is no channel to bind to, so the type assertion is replaced by an
  interface no type satisfies, and `getTLSCertificateHash` reports that channel
  binding is unavailable.

SCRAM-SHA-256 itself is untouched and works: it needs only `crypto/hmac`,
`crypto/sha256` and `crypto/pbkdf2`, all of which TinyGo implements. Verified
against PostgreSQL 17, which defaults to `scram-sha-256`.

## 4. `pgconn/errors.go` and `derived_types.go` — drop `regexp`

These two files were the only reason a TinyGo pgx binary linked the whole
regexp engine, and neither needs one:

- `redactPW` used three patterns (`password='[^']*'`, `password=[^ ]*`,
  `:[^:@]+?@`), compiled on every failed connect. They are replaced by three
  string-scanning helpers, checked equivalent against the originals over fixed
  cases and 200k random inputs.
- `serverVersion` compiled `^[0-9]+` per call just to take a digit prefix; a
  four-line scan does the same.

Behavior is unchanged; this patch exists for binary size (and stops
recompiling patterns per call on both paths).

## 5. `pgconn/pgpass_local.go` — drop the external pgpassfile dependency

`github.com/jackc/pgpassfile` keeps an unused package-level
`regexp.MustCompile`, which alone links the regexp engine back into every
TinyGo binary even after patch 4. The module's whole useful surface is a
40-line parser, so `pgpass_local.go` carries a package-local copy (checked
equivalent against upstream over escaped/wildcard/malformed lines) and
`config.go` calls it instead of the module. The std-Go backend still uses
upstream pgx and its pgpassfile, so the go.mod requirement stays.
