package netdev

import (
	"net/netip"
	"testing"
)

func TestNameserversFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want []string
	}{
		{"unset", "", nil},
		{"single", "10.0.0.53", []string{"10.0.0.53:53"}},
		{"explicit port", "10.0.0.53:5353", []string{"10.0.0.53:5353"}},
		{"comma separated", "10.0.0.53,10.0.0.54", []string{"10.0.0.53:53", "10.0.0.54:53"}},
		{"space separated", "10.0.0.53 10.0.0.54", []string{"10.0.0.53:53", "10.0.0.54:53"}},
		{"mixed forms", "10.0.0.53:5353,10.0.0.54", []string{"10.0.0.53:5353", "10.0.0.54:53"}},
		// A typo must not take the rest of the list down with it.
		{"skips junk", "nonsense,10.0.0.54", []string{"10.0.0.54:53"}},
		// IPv4 only, matching the rest of the package.
		{"skips ipv6", "2001:db8::1,10.0.0.54", []string{"10.0.0.54:53"}},
		{"all junk", "nonsense", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(NetdevDNSEnv, tc.env)
			got := nameserversFromEnv()
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i, w := range tc.want {
				if got[i] != netip.MustParseAddrPort(w) {
					t.Fatalf("[%d] got %v, want %v", i, got[i], w)
				}
			}
		})
	}
}

// TestNameserversEnvOverrides pins that the override replaces the discovered
// list rather than being appended to it. Appending would let a resolver that
// cannot answer internal names still be tried first.
func TestNameserversEnvOverrides(t *testing.T) {
	t.Setenv(NetdevDNSEnv, "10.0.0.53")
	got := nameservers()
	if len(got) != 1 || got[0] != netip.MustParseAddrPort("10.0.0.53:53") {
		t.Fatalf("nameservers() = %v, want only 10.0.0.53:53", got)
	}
}

// TestNameserversFallback pins the documented default when nothing is
// configured and no resolver file can be read.
func TestNameserversFallback(t *testing.T) {
	t.Setenv(NetdevDNSEnv, "")
	got := nameservers()
	if len(got) == 0 {
		t.Fatal("nameservers() returned nothing; a lookup would have no resolver at all")
	}
}
