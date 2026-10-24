//go:build tinygo && darwin

package rsasign

// Security.framework needs neither Clang blocks nor libdispatch here:
// SecKeyCreateSignature is a plain call. Only Security and CoreFoundation are
// linked, and internal/securetransport already links both on this platform.
//
// TinyGo ignores CGO_CFLAGS and CGO_LDFLAGS and rejects -F in CFLAGS, so the
// SDK location must be literal. The linker silently ignores a search path that
// does not exist, so the entries below are an ordered preference list: the
// Command Line Tools' MacOSX26.5.sdk, then the MacOSX.sdk of an Xcode install,
// then the MacOSX.sdk of the Command Line Tools.
//
// The MacOSX26.5.sdk entries come first on purpose. The macOS 27.0 SDK that
// Xcode 27.0 ships lists an arm64e.x1 target in every .tbd stub, and the
// ld64.lld bundled with TinyGo 0.42 and 0.43 (LLVM 22.1.4) rejects such files
// with "unknown architecture arm64e.x1-macos". The linker takes the first
// search path that has the file, so when the Command Line Tools still carry
// the 26.5 SDK it wins; where it is absent the entries are ignored and the
// current MacOSX.sdk is used as before. LLVM fixed the parser in 23.1.3;
// TinyGo tracks the cherry-pick in tinygo-org/tinygo#6019. Drop the 26.5
// entries once a TinyGo release links with the fixed lld.

/*
#cgo LDFLAGS: -L/Library/Developer/CommandLineTools/SDKs/MacOSX26.5.sdk/usr/lib
#cgo LDFLAGS: -L/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/lib
#cgo LDFLAGS: -L/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/usr/lib
#cgo LDFLAGS: -F/Library/Developer/CommandLineTools/SDKs/MacOSX26.5.sdk/System/Library/Frameworks
#cgo LDFLAGS: -F/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/System/Library/Frameworks
#cgo LDFLAGS: -F/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/System/Library/Frameworks
#cgo LDFLAGS: -framework Security -framework CoreFoundation
*/
import "C"
