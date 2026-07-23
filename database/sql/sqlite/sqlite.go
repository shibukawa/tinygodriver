// Package sqlite selects a database/sql SQLite backend appropriate for the
// active Go toolchain. Importing the package registers DriverName.
package sqlite

import "database/sql"

// DriverName is the portable database/sql driver name.
const DriverName = "sqlite"

// Open opens a database using the selected backend.
func Open(dsn string) (*sql.DB, error) { return sql.Open(DriverName, dsn) }
