//go:build tinygo && !(windows && cgo)

package netdev

import "net/netip"

// No system resolver is reachable on this build, so lookups fall through to
// the resolver list and the UDP query in dns.go.
//
// This is TinyGo on darwin and linux, and it is a linker limitation rather
// than a choice. getaddrinfo is absent from TinyGo's macos-minimal-sdk
// libSystem stubs -- linking it fails with "could not find symbol
// _getaddrinfo", and pointing the linker at a real SDK libSystem breaks the
// build instead. Linux is the same story from the other direction: TinyGo's
// linker does not expose the libc socket stubs, which is why sys_linux.go
// issues raw syscalls rather than calling libc at all.
//
// A link failure cannot be recovered from at run time, so attempting the call
// would break a build that currently works rather than degrade gracefully.
//
// The practical consequences on these builds:
//   - the DNS search list does not apply, so an unqualified short name fails
//   - resolvers a VPN scopes to a domain are not consulted
//
// NETDEV_DNS overrides the resolver list, and the hosts file is read first, so
// both remain available as workarounds. See
// requirement:windows-dns-resolution.
func systemLookupHost(name string) (netip.Addr, bool) {
	return netip.Addr{}, false
}
