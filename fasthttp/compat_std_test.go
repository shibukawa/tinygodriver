//go:build !tinygo

package fasthttp

// tlsServeError is what ServeTLSEmbed is expected to return. Standard Go serves
// TLS, so there is no error to expect.
func tlsServeError() error { return nil }
