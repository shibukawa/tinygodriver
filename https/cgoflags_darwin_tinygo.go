//go:build tinygo && darwin

package https

// TinyGo compiles cgo C files with -nostdlibinc against a bundled minimal
// macOS SDK, ignores CGO_CFLAGS and CGO_LDFLAGS, and rejects -F in CFLAGS. The
// SDK location therefore has to be literal here.
//
// Both standard SDK locations are listed. The linker silently ignores a search
// path that does not exist, so one entry covers Xcode installs and the other
// covers Command Line Tools, with no configuration from the user.
//
// -lsystem_blocks supplies the Clang blocks runtime, which TinyGo's minimal
// libSystem stub omits. The nw_* symbols come from Network.framework.

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
