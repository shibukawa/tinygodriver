//go:build !tinygo && !force_tinygo_logic

package pgxpool

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	ppool "github.com/jackc/pgx/v5/pgxpool"
)

// The public surface, as aliases so every value is the upstream pgxpool value
// itself. The vendored backend defines the same names against its own copy.
type (
	Pool                  = ppool.Pool
	Conn                  = ppool.Conn
	Config                = ppool.Config
	Stat                  = ppool.Stat
	Tx                    = ppool.Tx
	ShouldPingParams      = ppool.ShouldPingParams
	AcquireTracer         = ppool.AcquireTracer
	ReleaseTracer         = ppool.ReleaseTracer
	TraceAcquireStartData = ppool.TraceAcquireStartData
	TraceAcquireEndData   = ppool.TraceAcquireEndData
	TraceReleaseData      = ppool.TraceReleaseData
)

// parseConfig parses the DSN, pool parameters included, and installs the same
// per-connection defaults as database/pgx.ParseConfig. The watcher below is a
// copy of the one there: the two packages bind different underlying pgx
// packages per build, so the few lines cannot be shared without exporting
// them. Keep them in step.
func parseConfig(dsn string) (*Config, error) {
	cfg, err := ppool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.BuildContextWatcherHandler = cancelRequestWatcher
	return cfg, nil
}

func newWithConfig(ctx context.Context, cfg *Config) (*Pool, error) {
	return ppool.NewWithConfig(ctx, cfg)
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

// cancelDeadlineDelay bounds how long a cancelled query may keep its
// connection before the deadline is enforced as a fallback.
const cancelDeadlineDelay = 5 * time.Second
