//go:build (tinygo || force_tinygo_logic) && (wasip1 || wasip2 || nosqlite)

package sqlite

// Importing this package normally links the tinygosqlite C amalgamation, but
// that C does not compile on WASI targets (no pthread.h in the sysroot), and a
// project that never opens SQLite should not carry it either. This backend is
// selected there: the package still compiles and registers DriverName, and
// Open reports why there is no engine behind it. Build with -tags nosqlite to
// choose it deliberately on any TinyGo target.

import (
	"database/sql"
	"database/sql/driver"
	"errors"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "none"

var errNoBackend = errors.New("sqlite: no backend in this build (the C amalgamation does not compile on WASI targets, and -tags nosqlite excludes it deliberately)")

type noBackendDriver struct{}

func (noBackendDriver) Open(string) (driver.Conn, error) { return nil, errNoBackend }

func init() { sql.Register(DriverName, noBackendDriver{}) }

// driverInstance identifies this backend's driver for sqlbatch.RegisterSequential,
// which keys on the driver's type.
func driverInstance() driver.Driver { return noBackendDriver{} }
