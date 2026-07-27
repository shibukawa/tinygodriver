//go:build force_tinygo_logic && !tinygo && darwin && !darwinstarttlswith13

package https

// Host Go drives the system clang, which already knows the active SDK, so no
// explicit search paths are needed. This is the path
// `go test -tags force_tinygo_logic` takes on macOS.

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Network -framework Security -framework CoreFoundation
*/
import "C"
