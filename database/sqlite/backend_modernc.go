//go:build !tinygo && !force_tinygo_logic && !cgo

package sqlite

import _ "modernc.org/sqlite"

// Backend identifies the implementation selected by build constraints.
const Backend = "modernc"
