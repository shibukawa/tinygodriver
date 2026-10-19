//go:build goexperiment.simd && arm64 && !tinygo && !base64_c

package base64

import "simd/archsimd"

const vectorAvailable = true // NEON is part of the arm64 baseline.

func lookup(table, indices archsimd.Uint8x16) archsimd.Uint8x16 {
	return table.LookupOrZero(indices)
}

func allNonzero(x archsimd.Uint8x16) bool { return x.ReduceMin() != 0 }

// Classifier tables adapted from simdutf; see NOTICE. Control/space entries
// are cleared and the URL punctuation class rejects the standard alphabet.
// Every valid character maps to a single class bit. CLZ selects its decoding
// offset, including distinct classes for '+' and '/'; this avoids a separate
// comparison and correction for '/'. Control characters and space map to zero.
var highBits = [2][16]byte{
	{0, 0, 0, 0, 0, 6, 8, 8, 16, 32, 32, 16, 64, 128, 128, 64},
	{0, 0, 0, 0, 0, 4, 8, 8, 16, 32, 32, 18, 64, 128, 128, 64},
}
var lowBits = [2][16]byte{
	{169, 248, 248, 248, 248, 248, 248, 248, 248, 249, 241, 162, 161, 161, 160, 164},
	{169, 248, 248, 248, 248, 248, 248, 248, 248, 249, 241, 160, 161, 165, 160, 162},
}
var decodeOffsets = [2][16]byte{
	{185, 185, 191, 191, 4, 16, 19},
	{185, 185, 191, 191, 4, 17, 224},
}

func validateVector(x, lo, hi, mask archsimd.Uint8x16) (archsimd.Uint8x16, archsimd.Uint8x16) {
	valid := lo.LookupOrZero(x.And(mask)).And(hi.LookupOrZero(x.ShiftAllRight(3)))
	return valid, valid
}

func translateVector(x, class, delta, special, correction archsimd.Uint8x16) archsimd.Uint8x16 {
	return x.Add(delta.LookupOrZero(class.LeadingZeros()))
}

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
	return x.LookupOrZero(p.a).Shift(p.sa).Or(x.LookupOrZero(p.b).Shift(p.sb))
}

// Keep the value receiver within Go's four-field struct SSA limit. Adding
// a fifth vector here spills the whole packer to the stack on every iteration.
type encodePacker struct{ bytePacker }

func newEncodePacker() encodePacker {
	return encodePacker{bytePacker{archsimd.LoadUint8x16Array(&encodeA), archsimd.LoadUint8x16Array(&encodeB), archsimd.LoadInt8x16Array(&encodeShiftA), archsimd.LoadInt8x16Array(&encodeShiftB)}}
}

func (p encodePacker) pack(x, mask archsimd.Uint8x16) archsimd.Uint8x16 {
	return p.bytePacker.pack(x).And(mask)
}
