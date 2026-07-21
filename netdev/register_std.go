//go:build !tinygo

package netdev

// Standard Go uses the real net package; no netdev registration is needed.
func useNetdev(dev netdever) {}
