//go:build !((tinygo || bytealg_c) && cgo && (arm64 || amd64) && (darwin || linux || windows))

package bytealg

import "bytes"

// IndexByte uses Go's architecture-optimized standard implementation.
func IndexByte(b []byte, c byte) int { return bytes.IndexByte(b, c) }
