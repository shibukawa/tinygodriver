//go:build goexperiment.simd && arm64 && !tinygo && !base64_c

package base64

import "simd/archsimd"

const vectorAvailable = true // NEON is part of the arm64 baseline.

func lookup(table, indices archsimd.Uint8x16) archsimd.Uint8x16 {
	return table.LookupOrZero(indices)
}

func allNonzero(x archsimd.Uint8x16) bool { return x.ReduceMin() != 0 }

func highNibbles(x archsimd.Uint8x16) archsimd.Uint8x16 { return x.ShiftAllRight(4) }

var decodeA = [16]byte{0, 1, 2, 4, 5, 6, 8, 9, 10, 12, 13, 14, 255, 255, 255, 255}
var decodeB = [16]byte{1, 2, 3, 5, 6, 7, 9, 10, 11, 13, 14, 15, 255, 255, 255, 255}
var decodeShiftA = [16]int8{2, 4, 6, 2, 4, 6, 2, 4, 6, 2, 4, 6}
var decodeShiftB = [16]int8{-4, -2, 0, -4, -2, 0, -4, -2, 0, -4, -2, 0}
var encodeA = [16]byte{0, 0, 1, 2, 3, 3, 4, 5, 6, 6, 7, 8, 9, 9, 10, 11}
var encodeB = [16]byte{255, 1, 2, 255, 255, 4, 5, 255, 255, 7, 8, 255, 255, 10, 11, 255}
var encodeShiftA = [16]int8{-2, 4, 2, 0, -2, 4, 2, 0, -2, 4, 2, 0, -2, 4, 2, 0}
var encodeShiftB = [16]int8{0, -4, -6, 0, 0, -4, -6, 0, 0, -4, -6, 0, 0, -4, -6, 0}

// NEON's per-byte signed shifts pack directly into output order. Keeping
// shift vectors outside the loop also avoids Go 1.27's repeated broadcasts
// when lowering constant ShiftAll operations on 32-bit elements.
type bytePacker struct {
	a, b   archsimd.Uint8x16
	sa, sb archsimd.Int8x16
}

func newDecodePacker() bytePacker {
	return bytePacker{archsimd.LoadUint8x16Array(&decodeA), archsimd.LoadUint8x16Array(&decodeB), archsimd.LoadInt8x16Array(&decodeShiftA), archsimd.LoadInt8x16Array(&decodeShiftB)}
}
func (p bytePacker) pack(x archsimd.Uint8x16) archsimd.Uint8x16 {
	return lookup(x, p.a).Shift(p.sa).Or(lookup(x, p.b).Shift(p.sb))
}

type encodePacker struct {
	bytePacker
	mask archsimd.Uint8x16
}

func newEncodePacker() encodePacker {
	return encodePacker{bytePacker{archsimd.LoadUint8x16Array(&encodeA), archsimd.LoadUint8x16Array(&encodeB), archsimd.LoadInt8x16Array(&encodeShiftA), archsimd.LoadInt8x16Array(&encodeShiftB)}, archsimd.BroadcastUint8x16(63)}
}
func (p encodePacker) pack(x archsimd.Uint8x16) archsimd.Uint8x16 {
	return p.bytePacker.pack(x).And(p.mask)
}
