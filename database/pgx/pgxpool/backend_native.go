//go:build tinygo || force_tinygo_logic

package pgxpool

import (
	"context"
	"net"
	"time"

	"github.com/shibukawa/tinygodriver/database/internal/pgx/pgconn"
	"github.com/shibukawa/tinygodriver/database/internal/pgx/pgconn/ctxwatch"
	ppool "github.com/shibukawa/tinygodriver/database/internal/pgx/pgxpool"
	"github.com/shibukawa/tinygodriver/https"
)

// The public surface, same names as the std-go backend defines, bound here to
// the vendored copy. As in database/pgx, the aliases are the only access
// there is on this path: the vendored pgxpool sits under database/internal/,
// which no package outside database/ may import.
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
// per-connection defaults as database/pgx.ParseConfig. The watcher and dialer
// below are copies of the ones there: the two packages bind different
// underlying pgx packages per build, so the few lines cannot be shared
// without exporting them. Keep them in step.
func parseConfig(dsn string) (*Config, error) {
	cfg, err := ppool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.BuildContextWatcherHandler = cancelRequestWatcher
	cfg.ConnConfig.DialFunc = dialPlain
	return cfg, nil
}

func newWithConfig(ctx context.Context, cfg *Config) (*Pool, error) {
	return ppool.NewWithConfig(ctx, cfg)
}

// dialPlain replaces pgx's net.Dialer so the connection carries its
// descriptor, which is what lets sslmode start TLS on the already-connected
// socket; see database/pgx.
func dialPlain(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	return https.DialPlain(ctx, host, port)
}

// cancelRequestWatcher replaces pgx's default, which cancels by moving the
// deadline on the in-flight connection. netdev reads the deadline once when a
// read begins, so a later deadline change cannot interrupt it; sending a
// CancelRequest on a second connection is what actually works here.
func cancelRequestWatcher(c *pgconn.PgConn) ctxwatch.Handler {
	return &pgconn.CancelRequestContextWatcherHandler{
		Conn:          c,
		DeadlineDelay: cancelDeadlineDelay,
	}
}

// cancelDeadlineDelay bounds how long a cancelled query may keep its
// connection before the deadline is enforced as a fallback.
const cancelDeadlineDelay = 5 * time.Second
