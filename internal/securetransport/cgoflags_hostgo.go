//go:build !tinygo && darwin

package securetransport

// Host Go drives the system clang, which already knows the active SDK.
//
// Not gated behind force_tinygo_logic: netdev uses this package on every
// darwin build, including plain host Go.

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
*/
import "C"
