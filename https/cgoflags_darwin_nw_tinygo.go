//go:build tinygo && darwin && !darwinstarttlswith13

package https

// Only Network.framework is linked from this package now; Secure Transport
// moved to internal/securetransport, which carries its own flags. blocks and
// libdispatch are Network.framework's requirement, and TinyGo's minimal
// libSystem stub omits the blocks runtime, which is why it is linked here.
//
// TinyGo ignores CGO_CFLAGS and CGO_LDFLAGS and rejects -F in CFLAGS, so the
// SDK location must be literal. Both standard locations are listed; the linker
// silently ignores a search path that does not exist.

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -L/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/lib/system
#cgo LDFLAGS: -L/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/usr/lib/system
#cgo LDFLAGS: -L/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/lib
#cgo LDFLAGS: -L/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/usr/lib
#cgo LDFLAGS: -lsystem_blocks
#cgo LDFLAGS: -F/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/System/Library/Frameworks
#cgo LDFLAGS: -F/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/System/Library/Frameworks
#cgo LDFLAGS: -framework Network -framework Security -framework CoreFoundation
*/
import "C"
