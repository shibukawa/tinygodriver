//go:build !tinygo && !force_tinygo_logic && cgo

package sqlite

import (
	"database/sql"
	"database/sql/driver"

	mattn "github.com/mattn/go-sqlite3"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "mattn"

func init() { sql.Register(DriverName, &mattn.SQLiteDriver{}) }

// driverInstance identifies this backend's driver for sqlbatch.RegisterSequential,
// which keys on the driver's type.
func driverInstance() driver.Driver { return &mattn.SQLiteDriver{} }
