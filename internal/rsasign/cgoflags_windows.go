//go:build (tinygo || force_tinygo_logic) && windows

package rsasign

// bcrypt and crypt32 ship with Windows, so unlike the linux backend there is
// nothing to install and unlike the darwin backend there is no SDK path to
// guess. internal/schannel already links crypt32; listing it twice is harmless.

/*
#cgo LDFLAGS: -lbcrypt -lcrypt32
*/
import "C"
