//go:build tinygo || force_tinygo_logic

package stdlib

import (
	"database/sql"
	"database/sql/driver"

	"github.com/shibukawa/tinygodriver/database/internal/pgx/stdlib"
	pgx "github.com/shibukawa/tinygodriver/database/pgx"
)

// backendName identifies the pgx in use, for tests and diagnostics.
const backendName = "vendored"

// driverInstance identifies this backend's driver for sqlbatch.Register, which
// keys on the driver's type. stdlib.OpenDB hands that same type to every handle
// Open returns, so one instance is enough to name it.
func driverInstance() driver.Driver { return stdlib.GetDefaultDriver() }

// open adapts database/pgx to database/sql through the vendored pgx/stdlib.
// ParseConfig already installed the CancelRequest watcher and the fd-carrying
// dialer that makes sslmode work on this path, so this backend only picks
// which stdlib wraps the connection. See database/internal/PATCHES.md for what
// was changed in the vendored copy and why.
func open(dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return stdlib.OpenDB(*cfg), nil
}
