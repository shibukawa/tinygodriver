// Package pgx provides the pgx-native PostgreSQL API under both TinyGo and
// standard Go.
//
// It is the native sibling of database/sql/pgxstdlib, for code that wants
// pgx itself rather than database/sql: no pool mutex on the query path, no
// driver.Value boxing per parameter, and connection-oriented features such as
// Batch, CopyFrom and LISTEN/NOTIFY as first-class calls instead of an escape
// hatch behind a callback.
//
//	conn, err := pgx.Connect(ctx, "postgres://user:pass@localhost:5432/db?sslmode=disable")
//	if err != nil { ... }
//	defer conn.Close(ctx)
//
//	var n int
//	err = conn.QueryRow(ctx, "SELECT 1").Scan(&n)
//
// On standard Go every name here is an alias for the upstream
// github.com/jackc/pgx/v5 type, so a *pgx.Conn from this package is upstream's
// *pgx.Conn and passes to third-party code unchanged. On TinyGo the same names
// bind to a vendored copy of pgx v5.10.0 with its TLS use rerouted onto the
// platform's native stack, because TinyGo ships crypto/tls as a stub that
// cannot be linked. See database/internal/PATCHES.md. Code written against
// this package compiles identically on both.
//
// # Defaults
//
// ParseConfig and Connect install two defaults on every configuration:
//
//   - Query cancellation is performed by sending a CancelRequest on a second
//     connection, never by moving the read deadline. Under TinyGo's netdev a
//     deadline change cannot interrupt a blocked read, so the deadline
//     strategy would silently not cancel at all.
//   - On the TinyGo build, the dialer returns a connection that carries its
//     own file descriptor, which is what lets sslmode start TLS on the
//     already-connected socket.
//
// Both are plain fields on the returned ConnConfig, so a caller who needs
// different behavior may overwrite them before ConnectConfig.
//
// # sslmode
//
// sslmode is honored on both builds with the same semantics as
// pgxstdlib.Open. On TinyGo two differences from libpq are deliberate:
// verify-ca is treated as verify-full, and sslcert/sslkey are rejected rather
// than ignored, because the native TLS backends cannot offer a client
// certificate. A platform with no TLS backend refuses any mode but disable;
// it never falls back to plaintext silently.
//
// # TinyGo notes
//
// Build with -scheduler=threads. Under the cooperative scheduler a blocking
// socket call holds the whole runtime, so background goroutines never run and
// query cancellation silently stops working.
//
// Import netdev for its side effect, as with any TinyGo program using the
// network:
//
//	import _ "github.com/shibukawa/tinygodriver/netdev"
//
// Unix domain sockets and IPv6 are unavailable there, so connect over TCP to
// an IPv4 host.
//
// Registering custom pgtype codecs needs the pgtype package itself, which the
// TinyGo build keeps under internal/ and cannot re-export wholesale; that
// remains standard-Go-only for now. pgxpool is likewise not yet part of this
// surface.
package pgx

import "context"

// Connect opens a native pgx connection for a libpq-style URL or keyword DSN.
//
// Unlike database/sql handles, the connection is real and singular: it is
// established eagerly, is not safe for concurrent use, and belongs to the
// caller until Close. Use one connection per goroutine, or pool above this
// package.
func Connect(ctx context.Context, dsn string) (*Conn, error) {
	cfg, err := parseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return connectConfig(ctx, cfg)
}

// ConnectConfig opens a connection from a configuration built by ParseConfig.
// The config must originate from ParseConfig, which is pgx's own rule.
func ConnectConfig(ctx context.Context, cfg *ConnConfig) (*Conn, error) {
	return connectConfig(ctx, cfg)
}

// ParseConfig parses a libpq-style URL or keyword DSN and applies this
// package's defaults; see the package documentation. The result may be
// adjusted before ConnectConfig.
func ParseConfig(dsn string) (*ConnConfig, error) {
	return parseConfig(dsn)
}
