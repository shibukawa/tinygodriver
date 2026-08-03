# pgxstdlib

A PostgreSQL `database/sql` driver that works under TinyGo as well as standard
Go, so application code is shared between compilers.

```go
import "github.com/shibukawa/tinygodriver/database/sql/pgxstdlib"

db, err := pgxstdlib.Open("postgres://user:pass@localhost:5432/db?sslmode=verify-full")
```

Both builds are [pgx](https://github.com/jackc/pgx)'s `stdlib` driver:

| Build | Backend |
| --- | --- |
| Standard Go | upstream pgx, unmodified |
| TinyGo or `-tags force_tinygo_logic` | vendored pgx with TLS rerouted |

TinyGo ships `crypto/tls` as a stub that cannot be linked, so the vendored copy
routes TLS through the `https` package instead. That is three patched files out
of 145; see [internal/PATCHES.md](internal/PATCHES.md).

## TLS

`sslmode` works on both builds, including `verify-full` with a custom
`sslrootcert`. On TinyGo the handshake runs on the already-connected socket
after PostgreSQL's `SSLRequest`, using the platform's native TLS stack.

| Platform | Upgrade backend | Max version |
| --- | --- | --- |
| Standard Go | `crypto/tls` | TLS 1.3 |
| macOS | Secure Transport | TLS 1.2 |
| macOS with `-tags darwinstarttlswith13` | mbedTLS | TLS 1.3 |
| Linux | mbedTLS | TLS 1.3 |

Two differences from libpq are deliberate:

- `verify-ca` is treated as `verify-full`. libpq skips the host name check
  there; the native backends cannot express that, and checking the name too is
  stricter, never weaker.
- `sslcert` and `sslkey` are rejected rather than ignored, because the native
  backends cannot offer a client certificate.

A platform with no TLS backend refuses any mode but `disable`, and never falls
back to plaintext silently.

## TinyGo notes

Build with `-scheduler=threads`. Under the cooperative scheduler a blocking
socket call holds the whole runtime, so background goroutines never run and
query cancellation stops working without any error.

Blank-import `netdev`, as with any TinyGo program that uses the network:

```go
import _ "github.com/shibukawa/tinygodriver/netdev"
```

Unix domain sockets and IPv6 are unavailable there, so connect over TCP to an
IPv4 host.

## Cancellation

`context` cancellation is wired to PostgreSQL's `CancelRequest`, which opens a
second connection to ask the server to stop. pgx's default instead moves the
deadline on the in-flight connection, which does nothing under TinyGo: netdev
reads the deadline once when a read begins, so a later change cannot interrupt
it and the query runs to completion with no error. This package installs the
`CancelRequest` handler on both builds so behavior does not differ by compiler.

## Reaching pgx directly

`Batch`, `CopyFrom` and `LISTEN`/`NOTIFY` have no `database/sql` equivalent.
`WithConn` leases a pooled connection and hands over the pgx connection behind
it:

```go
err := pgxstdlib.WithConn(ctx, db, func(c *pgxstdlib.Conn) error {
	b := &pgxstdlib.Batch{}
	b.Queue("INSERT INTO t(a) VALUES ($1)", 1)
	b.Queue("INSERT INTO t(a) VALUES ($1)", 2)
	return c.SendBatch(ctx, b).Close()
})
```

`Conn`, `Batch`, `Rows`, `PgError` and the rest are aliases for the pgx types,
so the values are pgx's own. Going through this package is mandatory rather
than tidy: the TinyGo build uses the vendored pgx under `internal/`, which no
package outside `pgxstdlib` may import, so hand-writing the `sql.Conn.Raw`
assertion compiles on standard Go and fails on TinyGo with *use of internal
package not allowed*. `examples/pgxdemo` is built on both paths and is what
keeps that from regressing.

`c` is only valid inside the callback, as with `sql.Conn.Raw`. Read and close
`BatchResults` and `Rows` before returning.

## Updating the vendored pgx

```bash
go mod download github.com/jackc/pgx/v5@vX.Y.Z   # after bumping PGX_VERSION
python3 internal/vendor.py && python3 internal/patch.py
```

`patch.py` anchors every edit on an exact upstream string and fails loudly if
one is gone, so a version bump reports what needs attention instead of quietly
producing a tree that still links `crypto/tls`.

## Tests

The integration tests skip unless a server is configured:

```bash
docker run -d --name pgtest -e POSTGRES_PASSWORD=pass -e POSTGRES_USER=user \
    -e POSTGRES_DB=db -p 55432:5432 postgres:17

PGXSTDLIB_TEST_DSN='postgres://user:pass@localhost:55432/db?sslmode=disable' \
    go test ./database/sql/pgxstdlib/
```

Add `-tags force_tinygo_logic` to run the same suite against the vendored
backend without a TinyGo toolchain. The TLS tests need a server with `ssl=on`;
[tls_test.go](tls_test.go) documents the setup.
