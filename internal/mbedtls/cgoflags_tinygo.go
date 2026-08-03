//go:build tinygo && (linux || (darwin && darwinstarttlswith13))

package mbedtls

// TinyGo's clang resource directory omits arm_neon.h on every platform,
// because that header is generated during an LLVM build rather than checked
// into the clang source tree. MBEDTLS_TINYGO_NEON routes common.h to
// tinygo_arm_neon.h instead, which covers what mbedTLS uses with inline
// assembly.
//
// MBEDTLS_TINYGO_NO_INET_HEADERS stops x509_crt.c reaching for <arpa/inet.h>.
// TinyGo's musl has the header but does not export inet_pton, and its darwin
// SDK has the header but not the <netinet/in.h> it includes. Skipping it makes
// mbedTLS fall back to its own bundled implementation, which needs neither.
//
// No include path is needed: cgo puts the package directory on it, which is
// why the vendored headers live in mbedtls/ and psa/ right here. That matters
// because TinyGo does not expand ${SRCDIR} and ignores CGO_CFLAGS.

/*
#cgo CFLAGS: -DMBEDTLS_TINYGO_NEON -DMBEDTLS_TINYGO_NO_INET_HEADERS
*/
import "C"
