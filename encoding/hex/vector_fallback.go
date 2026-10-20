//go:build !((goexperiment.simd && (arm64 || amd64) && !tinygo && !hex_c) || ((tinygo || hex_c) && cgo && (arm64 || amd64) && (darwin || linux || windows)))

package hex

const vectorAvailable = false

func encodeVector(dst, src []byte) int { return 0 }
func decodeVector(dst, src []byte) int { return 0 }
