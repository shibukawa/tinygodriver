//go:build (tinygo || force_tinygo_logic) && ((linux && !wasip2) || (darwin && darwinstarttlswith13))

package https_test

import (
	"testing"

	"github.com/shibukawa/tinygodriver/internal/mbedtls"
)

// TestMbedTLSSelfTest runs the mbedTLS known-answer vectors. On arm64 this is
// what validates the bundled tinygo_arm_neon.h, so it is not optional: a
// hardware path that diverged from the reference would otherwise be invisible.
//
// The constraint above is deliberately the one internal/mbedtls itself carries,
// not the host-Go subset. Only a tinygo build defines MBEDTLS_TINYGO_NEON and
// reaches the hand-written header at all; a host build compiles the real
// <arm_neon.h> and proves nothing about it. Narrow this and the header goes
// back to being untested.
func TestMbedTLSSelfTest(t *testing.T) {
	if !mbedtls.Supported {
		t.Skip("mbedTLS backend not built")
	}
	if err := mbedtls.SelfTest(); err != nil {
		t.Fatalf("mbedTLS self test: %v", err)
		return
	}
}

// TestMbedTLSHWCaps is informational: it records what the CPU reports so a
// slow run can be told apart from a miscompiled one.
func TestMbedTLSHWCaps(t *testing.T) {
	if !mbedtls.Supported {
		t.Skip("mbedTLS backend not built")
	}
	caps := mbedtls.HWCaps()
	if caps < 0 {
		t.Log("CPU crypto: not reported on this platform")
		return
	}
	t.Logf("CPU crypto: AES=%v SHA256=%v SHA512=%v (raw %d)",
		caps&1 != 0, caps&2 != 0, caps&4 != 0, caps)
}
