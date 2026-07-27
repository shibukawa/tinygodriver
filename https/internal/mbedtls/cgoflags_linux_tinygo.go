//go:build tinygo && linux

package mbedtls

// TinyGo's clang resource directory omits arm_neon.h, because that header is
// generated during an LLVM build rather than checked into the clang source
// tree. MBEDTLS_TINYGO_NEON routes common.h to tinygo_arm_neon.h instead,
// which covers what mbedTLS uses with inline assembly.
//
// HTTPS_NEED_INET_PTON compiles the inet_pton shim in tls_mbedtls.c. TinyGo's
// musl does not export it, while host Go links glibc's, so it is TinyGo-only.
//
// No include path is needed: cgo puts the package directory on it, which is
// why the vendored headers live in mbedtls/ and psa/ right here. That matters
// because TinyGo does not expand ${SRCDIR} and ignores CGO_CFLAGS.

/*
#cgo CFLAGS: -DMBEDTLS_TINYGO_NEON -DHTTPS_NEED_INET_PTON
*/
import "C"
