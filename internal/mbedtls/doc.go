// Package mbedtls wraps a vendored mbedTLS for the TinyGo Linux HTTPS
// backend.
//
// TinyGo cannot link the OS OpenSSL at all: it emits no PT_INTERP, so dynamic
// relocations are never applied, and a static link needs roughly forty shim
// symbols whose set depends on how the distribution built OpenSSL. Compiling
// mbedTLS from source with TinyGo's own cgo sidesteps both problems, because
// the result is built against TinyGo's own musl and links statically.
//
// The vendored sources and the transformations applied to them are described
// in PATCHES.md and reproduced by vendor.py.
//
// This package is only built for TinyGo on Linux, or for host Go with the
// force_tinygo_logic tag. Everywhere else Supported reports false and the
// package contains no C.
package mbedtls
