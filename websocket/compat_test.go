package websocket

import (
	"math/rand"
	"testing"
)

// setFixedMaskKey makes the client frame mask reproducible, which upstream did
// with rand.Seed(1234).
//
// math/rand.Seed became a no-op in Go 1.24. Upstream's own test run escapes
// that only because its go.mod says `go 1.12` and GODEBUG defaults follow the
// module's Go version; vendored into this module it is a no-op, so a test
// comparing two masked frames byte for byte compares two different masks. A
// //go:debug randseednop=0 directive would fix the host build and do nothing
// under TinyGo, which does not implement godebug at all. Replacing the source
// that newMaskKey draws from is the only fix that works on both compilers.
//
// Calling this twice in one test is intended: each call installs a fresh
// sequence, which is what reseeding used to do.
func setFixedMaskKey(t *testing.T) {
	t.Helper()
	original := maskKeySource
	source := rand.New(rand.NewSource(1234))
	maskKeySource = source.Uint32
	t.Cleanup(func() { maskKeySource = original })
}

// TestBackend proves the build tags select exactly one implementation, and
// names it in the output so a failing run says which path it was on.
func TestBackend(t *testing.T) {
	switch Backend {
	case "std", "tinygo":
		t.Logf("websocket backend: %s", Backend)
	default:
		t.Errorf("unexpected backend %q", Backend)
	}
}
