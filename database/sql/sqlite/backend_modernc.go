//go:build !tinygo && !force_tinygo_logic && !cgo

package sqlite

import (
	"database/sql"
	"database/sql/driver"

	_ "modernc.org/sqlite"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "modernc"

// driverInstance identifies this backend's driver for sqlbatch.RegisterSequential,
// which keys on the driver's type.
//
// Unlike the other two backends, modernc registers its driver from its own init
// and exports no instance to name, so the only way to reach the value is to ask
// database/sql for it. sql.Open connects to nothing, so this costs a handle and
// no I/O.
func driverInstance() driver.Driver {
	db, err := sql.Open(DriverName, ":memory:")
	if err != nil {
		return nil
	}
	defer db.Close()
	return db.Driver()
}
