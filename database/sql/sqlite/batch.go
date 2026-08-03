package sqlite

import "github.com/shibukawa/tinygodriver/database/sql/sqlbatch"

// SQLite has no batch transport and needs none. There is no network, so there
// are no round trips to save; what costs is the fsync each autocommit statement
// pays, and running the queued statements in one transaction removes it.
//
// Measured on this repository's backends, 200 inserts to a file-backed database
// go from ~55ms to ~1ms that way, on both mattn and tinygosqlite.
//
// Registering for the sequential path also keeps the batch off multi-statement
// SQL entirely, which the three backends do not agree on.
func init() { sqlbatch.RegisterSequential(driverInstance()) }
