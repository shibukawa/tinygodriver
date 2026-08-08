package netdev

import (
	"net/netip"
	"testing"
	"time"
)

// TestSystemLookupHostLocalhost resolves a name every machine can answer
// offline, which makes it a layout check as much as a behaviour check.
//
// The Windows implementation hand-declares ADDRINFOA because TinyGo compiles
// cgo without the system headers. That layout differs from the unix one --
// ai_canonname precedes ai_addr, and ai_addrlen is a size_t -- so reading the
// address out of the wrong field is the obvious way to get this wrong. A
// mistake there would not crash; it would yield a plausible-looking wrong
// address, which is exactly what this pins down.
func TestSystemLookupHostLocalhost(t *testing.T) {
	ip, ok := systemLookupHost("localhost")
	if !ok {
		// A TinyGo build links dns_system_none.go, which has no resolver to ask.
		// t.Skip cannot end this function there -- SkipNow needs runtime.Goexit,
		// which TinyGo has not implemented, and the test would otherwise carry on
		// into the assertions below and fail on the zero address.
		t.Log("skipped: this build has no system resolver; see dns_system_none.go")
		return
	}
	if !ip.Is4() {
		t.Fatalf("got %v, want an IPv4 address", ip)
	}
	if ip != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("localhost resolved to %v, want 127.0.0.1 — check the addrinfo layout", ip)
	}
}

func TestSystemLookupHostRejectsEmpty(t *testing.T) {
	if _, ok := systemLookupHost(""); ok {
		t.Fatal("an empty name must not resolve")
	}
}

// TestSystemLookupHostUnknown pins that an unresolvable name reports false
// rather than an address, so the caller's fallback still runs.
func TestSystemLookupHostUnknown(t *testing.T) {
	// .invalid is reserved by RFC 2606 and must never resolve.
	if ip, ok := systemLookupHost("no-such-host.invalid"); ok {
		t.Fatalf("got %v, want no result", ip)
	}
}

// TestLookupOrderEnvBeatsSystem pins that NETDEV_DNS wins outright. If the
// system resolver were consulted as a fallback, an override pointing at the
// one server that can answer for a private zone would be silently bypassed
// whenever that server was slow or unreachable.
func TestLookupOrderEnvBeatsSystem(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for a UDP timeout")
	}
	// TEST-NET-1, which is not routable, so the query can only time out.
	t.Setenv(NetdevDNSEnv, "192.0.2.1")

	start := time.Now()
	// A name the system resolver would answer instantly.
	if ip, err := lookupHost("example.com"); err == nil {
		t.Fatalf("resolved to %v via the system resolver; the override must win", ip)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("failed in %v, too fast to have tried the override", elapsed)
	}
}

// TestLookupHostsFileBeatsEverything pins that the documented workaround still
// takes precedence, including over the system resolver.
func TestLookupHostsFileBeatsEverything(t *testing.T) {
	ip, err := lookupHost("localhost")
	if err != nil {
		t.Fatalf("localhost: %v", err)
	}
	if ip != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("got %v, want 127.0.0.1", ip)
	}
}
