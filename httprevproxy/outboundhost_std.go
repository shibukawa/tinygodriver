//go:build !tinygo && !force_tinygo_logic

package httprevproxy

import "net/http"

// fixOutboundHost is a no-op on standard Go, whose transport dials
// Request.URL.Host and treats Request.Host as the Host header alone. See the
// TinyGo build of this file for why the two need reconciling there.
func fixOutboundHost(*http.Request) error { return nil }
