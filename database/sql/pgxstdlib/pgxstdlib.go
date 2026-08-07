// Package pgxstdlib provides a PostgreSQL database/sql driver that works under
// both TinyGo and standard Go.
//
// Both builds are pgx/stdlib layered over database/pgx, which supplies the
// parsed configuration and its defaults. Standard Go uses upstream pgx
// unmodified; TinyGo uses a vendored copy with TLS removed, because TinyGo
// ships crypto/tls as a stub that cannot be linked. See
// database/internal/PATCHES.md. Code that wants pgx itself rather than
// database/sql should use database/pgx directly.
//
//	db, err := pgxstdlib.Open("postgres://user:pass@localhost:5432/db?sslmode=disable")
//	if err != nil { ... }
//	defer db.Close()
//
//	var n int
//	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&n)
//
// Everything database/sql offers works on both compilers: parameters, prepared
// statements, transactions, column metadata, and context cancellation.
//
// # Reaching pgx directly
//
// Batch, CopyFrom and LISTEN/NOTIFY have no database/sql equivalent. WithConn
// hands them the underlying pgx connection, on both compilers:
//
//	err := pgxstdlib.WithConn(ctx, db, func(c *pgxstdlib.Conn) error {
//		b := &pgxstdlib.Batch{}
//		b.Queue("INSERT INTO t(a) VALUES ($1)", 1)
//		b.Queue("INSERT INTO t(a) VALUES ($1)", 2)
//		return c.SendBatch(ctx, b).Close()
//	})
//
// The pgx types are re-exported here as aliases, so Conn is pgx's own Conn and
// takes pgx's own methods. Naming them through this package is not a style
// preference on TinyGo, it is the only option: that build uses the vendored pgx
// under internal/, which no package outside pgxstdlib may import. Writing the
// sql.Conn.Raw dance by hand works on standard Go and does not compile under
// TinyGo, because the type assertion has to name a type that is out of reach.
//
// Anything derived from c, including BatchResults and Rows, must be finished
// before the callback returns; see WithConn.
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
// Unix domain sockets and IPv6 are unavailable there, so connect over TCP to an
// IPv4 host. TLS support depends on the platform; see Open.
package pgxstdlib

import (
	"context"
	"database/sql"
)

// Open opens a database handle for a libpq-style URL or keyword DSN.
//
// The handle is lazy in the usual database/sql way: no connection is made until
// the first use. Call db.PingContext to verify the settings eagerly.
//
// sslmode is honored on both builds. On TinyGo it is served by the platform's
// native TLS stack, which starts TLS on the already-connected socket after
// PostgreSQL's SSLRequest, so verify-full and a custom sslrootcert both work.
// Two differences from libpq are deliberate:
//
//   - verify-ca is treated as verify-full. libpq would skip the host name
//     check; the native backends cannot express that, and checking the name as
//     well is stricter, never weaker.
//   - sslcert and sslkey are rejected rather than ignored, because the native
//     backends cannot offer a client certificate.
//
// A platform with no TLS backend refuses any mode but disable. It never falls
// back to plaintext silently.
func Open(dsn string) (*sql.DB, error) {
	return open(dsn)
}

// OpenContext is Open plus an eager connectivity check, so configuration errors
// surface at open time instead of at first query.
func OpenContext(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := open(dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
