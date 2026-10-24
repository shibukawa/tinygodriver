//go:build arm64 && !tinygo && !hex_c && !purego

package simdhex

// The Go assembler exposes NEON's deinterleaving load (LD2), interleaving
// store (ST2), four-register TBL and shift-insert (SLI), which simd/archsimd
// does not. This backend needs no GOEXPERIMENT; NEON is part of the arm64
// baseline.
const vectorAvailable = true

//go:noescape
func encodeNEON(dst, src []byte) int

//go:noescape
func decodeNEON(dst, src []byte) int

func encodeVector(dst, src []byte) int { return encodeNEON(dst, src) }
func decodeVector(dst, src []byte) int { return decodeNEON(dst, src) }
