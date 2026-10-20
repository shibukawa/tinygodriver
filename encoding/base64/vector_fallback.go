//go:build (!tinygo && !base64_c && ((arm64 && purego) || (amd64 && !goexperiment.simd) || (!arm64 && !amd64))) || ((tinygo || base64_c) && (!cgo || (!arm64 && !amd64) || (!darwin && !linux && !windows)))

package base64

const vectorAvailable = false

func decodeVector(dst, src []byte, url bool) (int, int) { return 0, 0 }
func encodeVector(dst, src []byte, url bool) int        { return 0 }
