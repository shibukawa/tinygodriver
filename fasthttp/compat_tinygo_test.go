//go:build tinygo

package fasthttp

// netdev is what gives TinyGo's net package a working socket layer on a host.
// Registering it is the application's job, not this package's, so the test does
// what a program using this fork has to do -- see the README.
import _ "github.com/shibukawa/tinygodriver/netdev"

// tlsServeError is what ServeTLSEmbed is expected to return: TinyGo cannot
// terminate TLS, and the fork must say so rather than serve cleartext.
func tlsServeError() error { return ErrTLSUnsupported }
