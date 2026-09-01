//go:build !tinygo && !force_tinygo_logic

package httprevproxy

// wantOutboundHost is the Host a Rewrite that called SetURL should leave on the
// outbound request. Standard net/http dials Request.URL.Host and reads an empty
// Request.Host as "take the Host header from the URL", so SetURL leaves it
// empty and nothing fills it in.
func wantOutboundHost(string) string { return "" }
