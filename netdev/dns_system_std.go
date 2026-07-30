//go:build !tinygo && !(windows && cgo)

package netdev

import (
	"net"
	"net/netip"
)

// systemLookupHost delegates to the standard library's resolver on host Go.
//
// This is safe only because register_std.go makes useNetdev a no-op outside
// TinyGo, so net does not route back into this package. Under TinyGo the same
// call would recurse forever, which is why that build gets a different file.
//
// It buys what a hand-rolled UDP query cannot: the search list, per-domain
// resolvers, and on macOS the System Configuration data that /etc/resolv.conf
// does not represent -- that file carries a header saying the system does not
// consult it.
func systemLookupHost(name string) (netip.Addr, bool) {
	if name == "" {
		return netip.Addr{}, false
	}
	addrs, err := net.LookupHost(name)
	if err != nil {
		return netip.Addr{}, false
	}
	for _, a := range addrs {
		ip, err := netip.ParseAddr(a)
		if err != nil {
			continue
		}
		// This package is IPv4-only, matching TinyGo's net port.
		if ip.Is4() {
			return ip, true
		}
		if ip.Is4In6() {
			return netip.AddrFrom4(ip.As4()), true
		}
	}
	return netip.Addr{}, false
}
