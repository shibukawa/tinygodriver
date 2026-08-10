//go:build tinygo

package websocket

// netdev is what gives TinyGo's net package a working socket layer on a host.
// Registering it is the application's job, not this package's, so the test does
// what a program using this fork has to do -- see the README.
import _ "github.com/shibukawa/tinygodriver/netdev"

// tlsDialError is what a wss:// dial is expected to return: TinyGo cannot
// originate TLS, and the fork must say so rather than panic inside tls.Client.
func tlsDialError() error { return ErrTLSUnsupported }
