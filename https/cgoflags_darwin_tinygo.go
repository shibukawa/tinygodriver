//go:build tinygo && darwin && !darwinstarttlswith13

package https

// This build carries both darwin backends: Network.framework for dialTLS and
// Secure Transport for upgradeTLS. Secure Transport needs neither Clang blocks
// nor libdispatch, but Network.framework does, so -fblocks and -lsystem_blocks
// are here for its sake. TinyGo's minimal libSystem stub omits the blocks
// runtime, which is why it has to be linked explicitly.
//
// TinyGo ignores CGO_CFLAGS and CGO_LDFLAGS and rejects -F in CFLAGS, so the
// SDK location must be literal here. Both standard locations are listed; the
// linker silently ignores a search path that does not exist, so one entry
// covers Xcode installs and the other covers Command Line Tools.

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
