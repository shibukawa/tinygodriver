//go:build goexperiment.simd && amd64 && !tinygo && !hex_c

package simdhex

import "simd/archsimd"

func encodeVector(dst, src []byte) int {
	encoder := newHexEncoder()
	i := 0
	for ; len(src)-i >= 16 && len(dst) >= 32; i += 16 {
		x := archsimd.LoadUint8x16Array((*[16]byte)(src[i:]))
		encoder.store((*[32]byte)(dst), x)
		dst = dst[32:]
	}
	return i
}

func decodeVector(dst, src []byte) int {
	space := archsimd.BroadcastUint8x16(32)
	zero := archsimd.BroadcastUint8x16('0')
	a := archsimd.BroadcastUint8x16('a')
	nine := archsimd.BroadcastUint8x16(9)
	five := archsimd.BroadcastUint8x16(5)
	mask := archsimd.BroadcastUint8x16(15)
	one := archsimd.BroadcastUint8x16(1)
	i := 0
	for ; len(src)-i >= 32 && len(dst) >= 16; i += 32 {
		p := (*[32]byte)(src[i:])
		x := archsimd.LoadUint8x16Array((*[16]byte)(p[:16]))
		y := archsimd.LoadUint8x16Array((*[16]byte)(p[16:]))
		dx, dy := x.Sub(zero), y.Sub(zero)
		ax, ay := x.Or(space).Sub(a).LessEqual(five), y.Or(space).Sub(a).LessEqual(five)
		vx := one.Masked(dx.LessEqual(nine).Or(ax))
		vy := one.Masked(dy.LessEqual(nine).Or(ay))
		// Validate the complete batch before any store, preserving the
		// standard decoder's partial output and first invalid byte.
		if !allValid(vx.And(vy)) {
			break
		}
		packHex(x.And(mask).Add(nine.Masked(ax)), y.And(mask).Add(nine.Masked(ay))).StoreArray((*[16]byte)(dst))
		dst = dst[16:]
	}
	return i
}
