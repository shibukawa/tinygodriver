//go:build windows && cgo

package schannel

// secur32, crypt32 and ncrypt ship with Windows, so unlike the linux backend
// there is nothing to install and unlike the darwin backend there is no SDK
// path to guess. ws2_32 is here for select, send and recv; netdev links it
// too, and listing it twice is harmless.
//
// Not gated behind force_tinygo_logic: netdev uses this package on every
// windows build, including plain host Go.

/*
#cgo LDFLAGS: -lsecur32 -lcrypt32 -lncrypt -lws2_32
*/
import "C"
