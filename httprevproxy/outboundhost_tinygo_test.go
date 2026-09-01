//go:build tinygo || force_tinygo_logic

package httprevproxy

// wantOutboundHost is the Host a Rewrite that called SetURL should leave on the
// outbound request. TinyGo dials Request.Host, so fixOutboundHost fills the
// empty Host in from the URL; the request on the wire is identical either way,
// because that is the Host header standard net/http would have written.
func wantOutboundHost(targetHost string) string { return targetHost }
