//go:build !tinygo && !force_tinygo_logic

package pgxstdlib

import (
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/stdlib"
)

// backendName identifies the pgx in use, for tests and diagnostics.
const backendName = "upstream"

// open uses upstream pgx, which needs no modification on this compiler.
func open(dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.BuildContextWatcherHandler = cancelRequestWatcher
	return stdlib.OpenDB(*cfg), nil
}

// cancelRequestWatcher replaces pgx's default, which cancels by moving the
// deadline on the in-flight connection. That does not work under TinyGo, and
// using the same handler on both compilers keeps cancellation behavior
// identical rather than platform-dependent.
func cancelRequestWatcher(c *pgconn.PgConn) ctxwatch.Handler {
	return &pgconn.CancelRequestContextWatcherHandler{
		Conn:          c,
		DeadlineDelay: cancelDeadlineDelay,
	}
}

// cancelDeadlineDelay bounds how long a cancelled query may keep its connection
// before the deadline is enforced as a fallback.
const cancelDeadlineDelay = 5 * time.Second
