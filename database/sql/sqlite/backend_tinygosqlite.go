//go:build tinygo || force_tinygo_logic

package sqlite

import _ "github.com/shibukawa/tinygodriver/database/sql/sqlite/tinygosqlite"

// Backend identifies the implementation selected by build constraints.
const Backend = "tinygosqlite"
