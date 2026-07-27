//go:build force_tinygo_logic && !tinygo && (linux || (darwin && darwinstarttlswith13))

package mbedtls

// Host Go has a complete toolchain, so common.h keeps the real <arm_neon.h>
// and x509_crt.c can use the platform inet_pton. tinygo_arm_neon.h is
// deliberately not selected here: its vector types use clang-specific
// attributes that GCC does not accept.
//
// This is the path `go test -tags force_tinygo_logic` takes.

/*
#cgo CFLAGS:
*/
import "C"
