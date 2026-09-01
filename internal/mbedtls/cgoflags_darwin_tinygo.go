//go:build tinygo && darwin && darwinstarttlswith13

package mbedtls

// TinyGo's darwin stdint.h is a 73-line minimal header: it declares the
// integer types but almost none of the limit macros that C99 requires.
// mbedTLS reads them for constant-time helpers, buffer bounds and the aarch64
// inline-asm pointer constraint, and hard errors without them.
//
// Each value comes from the matching clang predefine rather than a literal, so
// this stays correct on any target instead of hard-coding 64-bit. The *_MIN
// macros are omitted because cgo rejects the parentheses their expressions
// need, and mbedTLS does not reference them.
//
// Linux does not need this: TinyGo's musl stdint.h defines them itself, and
// defining them again there would be a redefinition.

// Three more darwin-only gaps, all of them the same shape: the C is fine, the
// symbol it ends up needing is not in TinyGo's minimal libSystem stub.
//
//   - _FORTIFY_SOURCE is on by default here, which rewrites memcpy into
//     __memcpy_chk. TinyGo's libSystem stub has no fortify symbols.
//   - bignum.c divides unsigned __int128, which needs compiler-rt's __udivti3.
//     That is not in the stub either. MBEDTLS_NO_UDBL_DIVISION is mbedTLS's own
//     switch for platforms without double-width division; it costs some bignum
//     speed and changes no results.
//   - ssl_tls.c walks a mbedtls_ecp_group_id array to its zero terminator.
//     The enum is four bytes wide, so the LLVM 22 that TinyGo 0.42 bundles
//     recognises that loop as wcslen() and emits the call; LLVM 20 under
//     TinyGo 0.41 did not. Linux is unaffected, because TinyGo's musl builds
//     wcslen. -fno-builtin-wcslen leaves the loop as a loop.

/*
#cgo CFLAGS: -D_FORTIFY_SOURCE=0 -DMBEDTLS_NO_UDBL_DIVISION
#cgo CFLAGS: -fno-builtin-wcslen
#cgo CFLAGS: -DUINTPTR_MAX=__UINTPTR_MAX__ -DINTPTR_MAX=__INTPTR_MAX__
#cgo CFLAGS: -DPTRDIFF_MAX=__PTRDIFF_MAX__
#cgo CFLAGS: -DUINT8_MAX=__UINT8_MAX__ -DUINT16_MAX=__UINT16_MAX__
#cgo CFLAGS: -DUINT32_MAX=__UINT32_MAX__ -DUINT64_MAX=__UINT64_MAX__
#cgo CFLAGS: -DINT8_MAX=__INT8_MAX__ -DINT16_MAX=__INT16_MAX__
#cgo CFLAGS: -DINT32_MAX=__INT32_MAX__ -DINT64_MAX=__INT64_MAX__
*/
import "C"
