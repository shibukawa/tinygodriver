// Package mysql provides a MySQL and MariaDB database/sql driver that works
// under both TinyGo and standard Go.
//
// Both builds are go-sql-driver/mysql. Standard Go uses it unmodified; TinyGo
// uses a fork under tinygomysql, because TinyGo's net.Conn supplies no
// descriptor for connection health checks or for an in-band TLS upgrade, and
// its crypto/tls is a stub. See tinygomysql/README.md.
//
//	db, err := mysql.Open("user:pass@tcp(127.0.0.1:3306)/app")
//	if err != nil { ... }
//	defer db.Close()
//
//	var n int
//	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&n)
//
// The DSN syntax, driver name and database/sql behavior are the same on both
// compilers: parameters, prepared statements, transactions, column metadata,
// and context cancellation.
//
// # TinyGo notes
//
// Use the threads scheduler, which is the default on desktop targets. Under the
// cooperative scheduler (-scheduler=tasks) a blocking socket call holds the
// whole runtime, so the driver's cancellation watcher never runs and
// QueryContext ignores its deadline without reporting an error.
//
// Import netdev for its side effect, as with any TinyGo program using the
// network:
//
//	import _ "github.com/shibukawa/tinygodriver/netdev"
//
// Unix domain sockets and IPv6 are unavailable there, so connect over TCP to an
// IPv4 host. The DSN timeout parameter has no effect, because TinyGo's
// net.Dialer ignores both its Timeout field and the context; readTimeout,
// writeTimeout and query-level deadlines all work.
package mysql

import (
	"context"
	"database/sql"
)

// DriverName is the portable database/sql driver name.
const DriverName = "mysql"

// Open opens a database handle for a go-sql-driver DSN.
//
// The handle is lazy in the usual database/sql way: no connection is made until
// the first use. Call db.PingContext to verify the settings eagerly, or use
// OpenContext.
//
// The tls parameter is honored on both builds. On TinyGo it is served by the
// platform's native TLS stack, which starts TLS on the already-connected socket
// after MySQL's capability exchange, so tls=true and a registered custom root
// both work. See RegisterTLSConfig.
func Open(dsn string) (*sql.DB, error) { return sql.Open(DriverName, dsn) }

// OpenContext is Open plus an eager connectivity check, so configuration errors
// surface at open time instead of at first query.
func OpenContext(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := Open(dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
