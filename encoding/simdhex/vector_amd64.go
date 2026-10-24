//go:build goexperiment.simd && amd64 && !tinygo && !hex_c

package simdhex

import "simd/archsimd"

var vectorAvailable = archsimd.X86.AVX2()

type hexEncoder struct{ mask, zero, nine, correction archsimd.Uint16x8 }

func newHexEncoder() hexEncoder {
	return hexEncoder{archsimd.BroadcastUint16x8(15), archsimd.BroadcastUint16x8('0'), archsimd.BroadcastUint16x8(9), archsimd.BroadcastUint16x8(39)}
}

func (e hexEncoder) digits(x archsimd.Uint16x8) archsimd.Uint8x16 {
	hi, lo := x.ShiftAllRight(4), x.And(e.mask)
	hi = hi.Add(e.zero).Add(e.correction.Masked(hi.Greater(e.nine)))
	lo = lo.Add(e.zero).Add(e.correction.Masked(lo.Greater(e.nine)))
	return hi.Or(lo.ShiftAllLeft(8)).ReshapeToUint8s()
}

func (e hexEncoder) store(dst *[32]byte, x archsimd.Uint8x16) {
	e.digits(x.ExtendLo8ToUint16()).StoreArray((*[16]byte)(dst[:16]))
	e.digits(x.ConcatShiftBytesRight(x, 8).ExtendLo8ToUint16()).StoreArray((*[16]byte)(dst[16:]))
}

func allValid(x archsimd.Uint8x16) bool {
	return x.Equal(archsimd.BroadcastUint8x16(0)).ToBits() == 0
}

var packWeights = [16]int8{16, 1, 16, 1, 16, 1, 16, 1, 16, 1, 16, 1, 16, 1, 16, 1}
var packOrder = [16]int8{0, 2, 4, 6, 8, 10, 12, 14, 1, 3, 5, 7, 9, 11, 13, 15}

// packHex joins each pair of nibble values into one byte. VPMADDUBSW forms
// x[2i]*16 + x[2i+1] in word i, at most 255, so it never saturates; y's words
// move to the high bytes and one VPSHUFB deinterleaves the result into x's
// eight bytes followed by y's. AVX2 has no unsigned word-to-byte narrowing:
// archsimd's SaturateToUint8 is VPMOVUSWB, an AVX-512 instruction, which
// faulted behind this file's AVX2 guard.
func packHex(x, y archsimd.Uint8x16) archsimd.Uint8x16 {
	weights := archsimd.LoadInt8x16Array(&packWeights)
	xw, yw := x.DotProductPairsSaturated(weights), y.DotProductPairsSaturated(weights)
	return xw.Or(yw.ShiftAllLeft(8)).AsUint8x16().PermuteOrZero(archsimd.LoadInt8x16Array(&packOrder))
}
