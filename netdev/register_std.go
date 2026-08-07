//go:build !tinygo || wasip1 || wasip2

package netdev

// Standard Go uses the real net package, and TinyGo's WASI targets ship a net
// package without the useNetdev hook; neither needs netdev registration.
func useNetdev(dev netdever) {}
