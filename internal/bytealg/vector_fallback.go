//go:build !((goexperiment.simd && (arm64 || amd64) && !tinygo && !bytealg_c) || ((tinygo || bytealg_c) && cgo && (arm64 || amd64) && (darwin || linux || windows)))

package bytealg

const Available = false

func maskVector(b []byte, key uint32) int { return 0 }
func headerVector(b []byte) bool          { return validHeaderScalar(b) }
