// Package pgxpool provides the pgx-native connection pool under both TinyGo
// and standard Go.
//
// It is the pool companion to database/pgx: on standard Go every name is an
// alias for the upstream github.com/jackc/pgx/v5/pgxpool type, so a *Pool
// from this package is upstream's *Pool and passes to third-party code
// unchanged; on TinyGo the same names bind to the vendored copy. A Pool is
// safe for concurrent use by many goroutines, which a bare pgx.Conn is not.
//
//	pool, err := pgxpool.New(ctx, "postgres://user:pass@localhost:5432/db?sslmode=disable")
//	if err != nil { ... }
//	defer pool.Close()
//
//	var n int
//	err = pool.QueryRow(ctx, "SELECT 1").Scan(&n)
//
// ParseConfig accepts the pool_* DSN parameters upstream documents
// (pool_max_conns, pool_min_conns, pool_max_conn_lifetime, ...), and installs
// the same per-connection defaults as database/pgx.ParseConfig: the
// CancelRequest cancellation watcher on both builds, and on TinyGo the
// fd-carrying dialer that makes sslmode work. Both sit on
// Config.ConnConfig as plain fields and may be overwritten before
// NewWithConfig.
//
// The TinyGo notes in database/pgx apply here unchanged: build with
// -scheduler=threads, import netdev for its side effect, TCP over IPv4 only.
package pgxpool

import "context"

// New creates a Pool for a libpq-style URL or keyword DSN, which may carry
// the pool_* parameters. Connections are established lazily; use Ping to
// verify the configuration eagerly.
func New(ctx context.Context, dsn string) (*Pool, error) {
	cfg, err := parseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return newWithConfig(ctx, cfg)
}

// NewWithConfig creates a Pool from a configuration built by ParseConfig.
// The config must originate from ParseConfig, which is pgxpool's own rule.
func NewWithConfig(ctx context.Context, cfg *Config) (*Pool, error) {
	return newWithConfig(ctx, cfg)
}

// ParseConfig parses a DSN including the pool_* parameters and applies this
// package's per-connection defaults; see the package documentation. The
// result may be adjusted before NewWithConfig.
func ParseConfig(dsn string) (*Config, error) {
	return parseConfig(dsn)
}
