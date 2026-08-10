//go:build !tinygo && !force_tinygo_logic

package httpserver

import (
	"net"
	"net/http"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "std"

// serve delegates to net/http. Hijack works under standard Go, so a handler can
// take over the connection with nothing in front of it, and adding a layer here
// would only cost a read and a copy per connection.
//
// The head timeout is the one thing Config carries onto this path, because
// net/http can enforce it itself and defaults to no limit.
func serve(ln net.Listener, srv *http.Server, cfg Config) error {
	if srv.ReadHeaderTimeout == 0 && cfg.ReadHeaderTimeout > 0 {
		srv.ReadHeaderTimeout = cfg.ReadHeaderTimeout
	}
	return srv.Serve(ln)
}
