//go:build tinygo || force_tinygo_logic

package sqlite

import (
	"database/sql/driver"

	"github.com/shibukawa/tinygodriver/database/sql/sqlite/tinygosqlite"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "tinygosqlite"

// driverInstance identifies this backend's driver for sqlbatch.RegisterSequential,
// which keys on the driver's type.
func driverInstance() driver.Driver { return tinygosqlite.Driver }
