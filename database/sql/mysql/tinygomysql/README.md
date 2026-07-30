# tinygomysql

`tinygomysql` is a fork of [go-sql-driver/mysql](https://github.com/go-sql-driver/mysql)
v1.10.0, carried here because two things it relies on do not exist under TinyGo.
It speaks MySQL and MariaDB over `database/sql` and registers the driver name
`mysql`, exactly as upstream does.

Import the higher-level `database/sql/mysql` package unless a direct test of
this backend is required. That package selects this fork on TinyGo and
`force_tinygo_logic` builds, and the unmodified upstream driver everywhere else.

## Licence

**This directory is Mozilla Public License 2.0**, not the Apache 2.0 of the rest
of the repository. MPL-2.0 is a file-level copyleft: these files stay under it,
combining them with Apache-2.0 code is fine, and modifications to them must be
published under MPL-2.0. `LICENSE` and `AUTHORS` are upstream's own.

Every change to upstream source is marked with a `PETITWEB:` comment so a diff
against v1.10.0 stays readable.

## Divergences from upstream

### Connection pooling

`connCheck` asks `net.Conn` for a `syscall.Conn` and calls `SyscallConn`.
TinyGo's `net.TCPConn` implements the interface but returns an error, so every
pooled connection was judged dead and re-dialed. Measured before the fix: 200
sequential `SELECT 1` opened **201** server connections and took 190 ms; after,
**0** and 34 ms, matching standard Go.

`conncheck.go` gains `&& !tinygo` and `conncheck_dummy.go` gains `tinygo ||`.
The constraint is on `tinygo` alone, not `force_tinygo_logic`: a
`force_tinygo_logic` build runs on host Go, whose `net.Conn` supports
`SyscallConn` normally.

### TLS

TinyGo ships a stub `crypto/tls` whose `Client()` **panics**, so upstream's
handshake could not run at all. MySQL upgrades in band — plaintext capability
packet first, then TLS on the very same socket — which rules out any TLS API
that owns its own dialing.

TLS therefore goes through the `https.DialPlain` / `https.Upgrade` seam, backed
by the OS TLS stack: Secure Transport on macOS, mbedTLS on Linux, Schannel on
Windows. `crypto/tls` is no longer imported anywhere in this package.

Consequences for callers:

- `Config.TLS` is a `*https.Config` rather than a `*tls.Config`. It takes PEM
  bytes, the one representation every backend accepts.
- `RegisterTLSConfig` takes a `*https.Config`. Prefer
  `database/sql/mysql.RegisterTLSConfig`, which has the same signature on both
  backends and converts for upstream.
- A `tls=` DSN requires the driver's own dialer, because the handshake needs the
  socket descriptor and TinyGo's `net.TCPConn` will not surrender one. Setting
  `DialFunc` or `RegisterDialContext` alongside `tls=` returns
  `https.ErrNotUpgradable` instead of silently connecting in cleartext.
- macOS caps at TLS 1.2 on this path; Apple never added 1.3 to Secure Transport.
  Build with `-tags darwinstarttlswith13` to use mbedTLS and get 1.3.
- Client certificates are unsupported on macOS, as elsewhere in `https`.

## Known limitations under TinyGo

- **The cooperative scheduler breaks cancellation.** Under
  `-scheduler=tasks` a blocking socket call holds the runtime, so the watcher
  goroutine that cancels a query never runs and the deadline is ignored with no
  error. The threads scheduler is the default on desktop targets.
- **Unix sockets are unavailable.** TinyGo's `net` supports `tcp` only.
- **DSN `timeout=` has no effect.** TinyGo's `net.Dialer.DialContext` ignores
  both `Timeout` and the context. `readTimeout`, `writeTimeout` and
  context deadlines on queries do work.
- **`LocalAddr()` is nil** on a dialed connection, so log lines read
  `read tcp <nil>->host:port`.

## Verified

TinyGo 0.41.1 on darwin/arm64 against MariaDB 11.8 and MySQL 8.4: all types,
`parseTime`, prepared statements, `interpolateParams`, transactions, context
cancellation, 16-goroutine concurrency, 4 MB `LONGBLOB`, `multiStatements`,
`compress=true`, `caching_sha2_password` full authentication including RSA, and
MariaDB `ed25519`. TLS verified against a MariaDB configured with a private CA:
`skip-verify`, `preferred`, a registered custom CA, and rejection of both a
mismatched hostname and an untrusted root.
