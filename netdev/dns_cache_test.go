package netdev

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDNSCacheTTLBounds(t *testing.T) {
	var c dnsCache
	ip := netip.MustParseAddr("192.0.2.10")

	c.put("short.example", ip, 0)
	if got, ok := c.get("short.example"); !ok || got != ip {
		t.Fatalf("zero-TTL answer not held for the minimum: %v %v", got, ok)
	}

	c.put("long.example", ip, 24*time.Hour)
	c.mu.Lock()
	var expires time.Time
	for _, entry := range c.entries {
		if entry.name == "long.example" {
			expires = entry.expires
		}
	}
	c.mu.Unlock()
	if until := time.Until(expires); until > dnsCacheMaxTTL+time.Minute {
		t.Fatalf("TTL not capped: expires in %v", until)
	}

	if _, ok := c.get("absent.example"); ok {
		t.Fatal("hit for a name never stored")
	}
}

func TestDNSCacheExpiryAndReplacement(t *testing.T) {
	var c dnsCache
	ip := netip.MustParseAddr("192.0.2.11")
	c.put("x.example", ip, time.Second)
	c.mu.Lock()
	for i := range c.entries {
		if c.entries[i].name == "x.example" {
			c.entries[i].expires = time.Now().Add(-time.Second)
		}
	}
	c.mu.Unlock()
	if _, ok := c.get("x.example"); ok {
		t.Fatal("expired entry served")
	}

	// Re-storing a name replaces its entry rather than appending forever.
	other := netip.MustParseAddr("192.0.2.12")
	c.put("y.example", ip, time.Minute)
	c.put("y.example", other, time.Minute)
	if got, _ := c.get("y.example"); got != other {
		t.Fatalf("replacement not visible: %v", got)
	}
	count := 0
	c.mu.Lock()
	for _, entry := range c.entries {
		if entry.name == "y.example" {
			count++
		}
	}
	c.mu.Unlock()
	if count != 1 {
		t.Fatalf("y.example stored %d times", count)
	}
}

func TestLookupHostsInRevalidatesOnChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	write := func(content string, when time.Time) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		// Force distinct mtimes; some filesystems are coarse enough that two
		// writes in one test land on the same tick.
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}

	write("192.0.2.1 first.example\n", time.Now().Add(-2*time.Second))
	if ip, ok := lookupHostsIn(path, "first.example"); !ok || ip != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("first parse: %v %v", ip, ok)
	}
	// Unchanged file: served from the cache.
	if _, ok := lookupHostsIn(path, "first.example"); !ok {
		t.Fatal("cached parse missing")
	}

	write("192.0.2.2 second.example\n", time.Now())
	if _, ok := lookupHostsIn(path, "first.example"); ok {
		t.Fatal("edited file served from stale cache")
	}
	if ip, ok := lookupHostsIn(path, "second.example"); !ok || ip != netip.MustParseAddr("192.0.2.2") {
		t.Fatalf("reparse: %v %v", ip, ok)
	}

	if _, ok := lookupHostsIn(filepath.Join(t.TempDir(), "missing"), "first.example"); ok {
		t.Fatal("missing file reported a hit")
	}
}

func TestParseHostsFileFirstEntryWins(t *testing.T) {
	entries := parseHostsFile([]byte(
		"192.0.2.1 dup.example # comment\n" +
			"192.0.2.2 dup.example other.example\n" +
			"not-an-ip dup.example\n" +
			"2001:db8::1 v6.example\n"))
	lookup := func(name string) (netip.Addr, bool) {
		for _, entry := range entries {
			if entry.name == name {
				return entry.ip, true
			}
		}
		return netip.Addr{}, false
	}
	if ip, _ := lookup("dup.example"); ip != netip.MustParseAddr("192.0.2.1") {
		t.Errorf("dup.example = %v, want first entry", ip)
	}
	if ip, _ := lookup("other.example"); ip != netip.MustParseAddr("192.0.2.2") {
		t.Errorf("other.example = %v", ip)
	}
	if _, ok := lookup("v6.example"); ok {
		t.Error("IPv6 entry accepted on an IPv4-only path")
	}
}

func TestWrappedErrorMatchesBothEnds(t *testing.T) {
	cause := errors.New("underlying failure")
	err := &wrappedError{sentinel: ErrHostUnknown, cause: cause}
	if !errors.Is(err, ErrHostUnknown) || !errors.Is(err, cause) {
		t.Fatalf("errors.Is missed a wrapped end: %v", err)
	}
	if want := "Host unknown: underlying failure"; err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}
