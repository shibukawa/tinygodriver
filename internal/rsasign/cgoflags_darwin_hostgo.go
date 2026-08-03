//go:build force_tinygo_logic && !tinygo && darwin

package rsasign

// Host Go drives the system clang, which already knows the active SDK.
//
// Gated on force_tinygo_logic because a plain host-go build uses crypto/rsa and
// must not compile or link any of this.

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
*/
import "C"
