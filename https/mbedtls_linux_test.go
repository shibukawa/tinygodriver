//go:build force_tinygo_logic && !tinygo && linux

package https_test

import (
	"testing"

	"github.com/shibukawa/tinygodriver/https/internal/mbedtls"
)

// TestMbedTLSSelfTest runs the mbedTLS known-answer vectors. On arm64 this is
// what validates the bundled tinygo_arm_neon.h, so it is not optional: a
// hardware path that diverged from the reference would otherwise be invisible.
func TestMbedTLSSelfTest(t *testing.T) {
	if !mbedtls.Supported {
		t.Skip("mbedTLS backend not built")
	}
	if err := mbedtls.SelfTest(); err != nil {
		t.Fatalf("mbedTLS self test: %v", err)
	}
}

// TestMbedTLSHWCaps is informational: it records what the CPU reports so a
// slow run can be told apart from a miscompiled one.
func TestMbedTLSHWCaps(t *testing.T) {
	if !mbedtls.Supported {
		t.Skip("mbedTLS backend not built")
	}
	caps := mbedtls.HWCaps()
	t.Logf("CPU crypto: AES=%v SHA256=%v SHA512=%v (raw %d)",
		caps&1 != 0, caps&2 != 0, caps&4 != 0, caps)
}
