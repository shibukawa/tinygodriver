# pgx

PostgreSQL drivers that work under TinyGo as well as standard Go, so
application code is shared between compilers. The layout mirrors upstream
[pgx](https://github.com/jackc/pgx):

| Package | Mirrors | Use it for |
| --- | --- | --- |
| `database/pgx` | `github.com/jackc/pgx/v5` | The pgx-native API: `Connect`, `Batch`, `CopyFrom`, `LISTEN`/`NOTIFY`, row-collection helpers |
| `database/pgx/pgxpool` | `pgx/v5/pgxpool` | A concurrency-safe pool of native connections |
| `database/pgx/stdlib` | `pgx/v5/stdlib` | The `database/sql` adapter |

On standard Go every exported name is an alias for the upstream pgx type, so
values interoperate with third-party code that names pgx types. On TinyGo the
same names bind to a vendored copy of pgx v5.10.0 with TLS rerouted, because
TinyGo ships `crypto/tls` as a stub that cannot be linked. That is three
patched files out of 145; see
[../internal/PATCHES.md](../internal/PATCHES.md).

```go
import (
	pgx "github.com/shibukawa/tinygodriver/database/pgx"
	"github.com/shibukawa/tinygodriver/database/pgx/pgxpool"
	"github.com/shibukawa/tinygodriver/database/pgx/stdlib"
)

// Native, one connection:
conn, err := pgx.Connect(ctx, dsn)

// Native, pooled:
pool, err := pgxpool.New(ctx, dsn)

// database/sql:
db, err := stdlib.Open(dsn)
```

`ParseConfig` in all three installs the same defaults: cancellation via
`CancelRequest`, and on TinyGo the fd-carrying dialer that makes `sslmode`
work. Prefer the native surface when performance matters: the `database/sql`
layer costs a pool mutex and a per-connection mutex on every call plus one
`driver.Value` boxing per parameter and result (measured: 15 vs 6 allocations
per query).

## TLS

`sslmode` works on all surfaces and both builds, including `verify-full` with
a custom `sslrootcert`. On TinyGo the handshake runs on the already-connected
socket after PostgreSQL's `SSLRequest`, using the platform's native TLS stack.

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
back to plaintext silently. One known limit: `CopyFrom` over TLS on the TinyGo
path can stall, because the native TLS sessions serialize reads against
writes; plaintext `CopyFrom` is full duplex and verified.

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
it and the query runs to completion with no error. `ParseConfig` installs the
`CancelRequest` handler on both builds so behavior does not differ by
compiler.

## Reaching pgx from database/sql

`Batch`, `CopyFrom` and `LISTEN`/`NOTIFY` have no `database/sql` equivalent.
For code that lives on `database/sql`, `stdlib.WithConn` leases a pooled
connection and hands over the pgx connection behind it:

```go
err := stdlib.WithConn(ctx, db, func(c *pgx.Conn) error {
	b := &pgx.Batch{}
	b.Queue("INSERT INTO t(a) VALUES ($1)", 1)
	b.Queue("INSERT INTO t(a) VALUES ($1)", 2)
	return c.SendBatch(ctx, b).Close()
})
```

Naming the types through `database/pgx` is mandatory rather than tidy: the
TinyGo build uses the vendored pgx under `database/internal/`, which no
package outside `database/` may import, so hand-writing the `sql.Conn.Raw`
assertion compiles on standard Go and fails on TinyGo with *use of internal
package not allowed*. `examples/pgxdemo` and `examples/pgxnativedemo` are
built on both paths and are what keep that from regressing.

`c` is only valid inside the callback, as with `sql.Conn.Raw`. Read and close
`BatchResults` and `Rows` before returning.

## Updating the vendored pgx

```bash
go mod download github.com/jackc/pgx/v5@vX.Y.Z   # after bumping PGX_VERSION
python3 ../internal/vendor.py && python3 ../internal/patch.py
```

`patch.py` anchors every edit on an exact upstream string and fails loudly if
one is gone, so a version bump reports what needs attention instead of quietly
producing a tree that still links `crypto/tls`.

## Tests

The integration tests skip unless a server is configured:

```bash
docker run -d --name pgtest -e POSTGRES_PASSWORD=pass -e POSTGRES_USER=user \
    -e POSTGRES_DB=db -p 55432:5432 postgres:17

PGX_TEST_DSN='postgres://user:pass@localhost:55432/db?sslmode=disable' \
    go test ./database/pgx/...
```

Add `-tags force_tinygo_logic` to run the same suites against the vendored
backend without a TinyGo toolchain. The TLS tests need a server with `ssl=on`;
[stdlib/tls_test.go](stdlib/tls_test.go) documents the setup.
