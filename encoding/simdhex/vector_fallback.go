//go:build (!tinygo && !hex_c && ((arm64 && purego) || (amd64 && !goexperiment.simd) || (!arm64 && !amd64))) || ((tinygo || hex_c) && (!cgo || (!arm64 && !amd64) || (!darwin && !linux && !windows)))

package simdhex

const vectorAvailable = false

func encodeVector(dst, src []byte) int { return 0 }
func decodeVector(dst, src []byte) int { return 0 }
