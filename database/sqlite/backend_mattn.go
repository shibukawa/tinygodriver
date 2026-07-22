//go:build !tinygo && !force_tinygo_logic && cgo

package sqlite

import (
	"database/sql"

	mattn "github.com/mattn/go-sqlite3"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "mattn"

func init() { sql.Register(DriverName, &mattn.SQLiteDriver{}) }
