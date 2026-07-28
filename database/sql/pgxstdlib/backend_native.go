//go:build tinygo || force_tinygo_logic

package pgxstdlib

import (
	"context"
	"database/sql"
	"net"
	"time"

	pgx "github.com/shibukawa/tinygodriver/database/sql/pgxstdlib/internal/pgx"
	"github.com/shibukawa/tinygodriver/database/sql/pgxstdlib/internal/pgx/pgconn"
	"github.com/shibukawa/tinygodriver/database/sql/pgxstdlib/internal/pgx/pgconn/ctxwatch"
	"github.com/shibukawa/tinygodriver/database/sql/pgxstdlib/internal/pgx/stdlib"
	"github.com/shibukawa/tinygodriver/https"
)

// backendName identifies the pgx in use, for tests and diagnostics.
const backendName = "vendored"

// open uses the vendored pgx. It differs from the standard-Go path only in
// which stdlib is imported, plus the dialer below; see internal/PATCHES.md for
// what was changed and why.
func open(dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.BuildContextWatcherHandler = cancelRequestWatcher
	cfg.DialFunc = dialPlain
	return stdlib.OpenDB(*cfg), nil
}

// dialPlain replaces pgx's net.Dialer so the connection carries its descriptor.
//
// TinyGo's net.TCPConn.SyscallConn returns an error, so a descriptor cannot be
// recovered from an arbitrary net.Conn, and starting TLS on an already
// connected socket needs one. https.DialPlain returns a connection that carries
// its own, which is what makes sslmode work at all on this path.
func dialPlain(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	return https.DialPlain(ctx, host, port)
}

// cancelRequestWatcher replaces pgx's default, which cancels by moving the
// deadline on the in-flight connection. netdev reads the deadline once when a
// read begins and then blocks in select, so a later deadline change cannot
// interrupt it: the query would run to completion and return no error at all.
// Sending a CancelRequest on a second connection is what actually works here.
func cancelRequestWatcher(c *pgconn.PgConn) ctxwatch.Handler {
	return &pgconn.CancelRequestContextWatcherHandler{
		Conn:          c,
		DeadlineDelay: cancelDeadlineDelay,
	}
}

// cancelDeadlineDelay bounds how long a cancelled query may keep its connection
// before the deadline is enforced as a fallback.
const cancelDeadlineDelay = 5 * time.Second
