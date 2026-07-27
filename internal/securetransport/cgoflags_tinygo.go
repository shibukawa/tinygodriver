//go:build tinygo && darwin

package securetransport

// Secure Transport needs neither Clang blocks nor libdispatch: SSLSetIOFuncs
// takes plain function pointers. Only Security and CoreFoundation are linked.
//
// TinyGo ignores CGO_CFLAGS and CGO_LDFLAGS and rejects -F in CFLAGS, so the
// SDK location must be literal here. Both standard locations are listed; the
// linker silently ignores a search path that does not exist, so one entry
// covers Xcode installs and the other covers Command Line Tools.

/*
#cgo LDFLAGS: -L/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/lib
#cgo LDFLAGS: -L/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/usr/lib
#cgo LDFLAGS: -F/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/System/Library/Frameworks
#cgo LDFLAGS: -F/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/System/Library/Frameworks
#cgo LDFLAGS: -framework Security -framework CoreFoundation
*/
import "C"
