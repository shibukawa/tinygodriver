//go:build goexperiment.simd && amd64 && !tinygo && !hex_c

package hex

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

func packHex(x, y archsimd.Uint8x16) archsimd.Uint8x16 {
	xw, yw := x.ReshapeToUint16s(), y.ReshapeToUint16s()
	mask := archsimd.BroadcastUint16x8(255)
	xp := xw.ShiftAllLeft(4).Or(xw.ShiftAllRight(8)).And(mask).SaturateToUint8()
	yp := yw.ShiftAllLeft(4).Or(yw.ShiftAllRight(8)).And(mask).SaturateToUint8()
	return xp.ReshapeToUint64s().InterleaveLo(yp.ReshapeToUint64s()).ReshapeToUint8s()
}
