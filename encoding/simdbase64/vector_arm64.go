//go:build arm64 && !tinygo && !base64_c && !purego

package simdbase64

// The Go assembler exposes NEON's structured loads/stores (LD3/LD4/ST3/ST4),
// four-register TBL and shift-insert, which simd/archsimd does not. This
// backend needs no GOEXPERIMENT; NEON is part of the arm64 baseline.
const vectorAvailable = true

//go:noescape
func decodeNEON(dst, src []byte, url bool) (read, written int)

//go:noescape
func encodeNEON(dst, src []byte, url bool) int

func decodeVector(dst, src []byte, url bool) (int, int) { return decodeNEON(dst, src, url) }
func encodeVector(dst, src []byte, url bool) int        { return encodeNEON(dst, src, url) }
