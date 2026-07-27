//go:build force_tinygo_logic && !tinygo && darwin && darwintls13

package https

// Host Go drives the system clang, which already knows the active SDK, so the
// native backend needs no explicit SDK search paths here. This is the path
// used by `go test -tags force_tinygo_logic`.

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Network -framework Security -framework CoreFoundation
*/
import "C"
