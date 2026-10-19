//go:build goexperiment.simd && amd64 && !tinygo && !base64_c

package base64

import "simd/archsimd"

// Guard every entry to the AVX kernel, including on GOAMD64=v1 builds.
var vectorAvailable = archsimd.X86.AVX2()

func lookup(table, indices archsimd.Uint8x16) archsimd.Uint8x16 {
	// Callers provide indices in 0..15 or 0xff (zero fill), so the x86
	// modulo-16 behavior and ARM's out-of-range behavior agree here.
	return table.PermuteOrZero(indices.BitsToInt8())
}

func allNonzero(x archsimd.Uint8x16) bool {
	return x.Equal(archsimd.BroadcastUint8x16(0)).ToBits() == 0
}

func highNibbles(x archsimd.Uint8x16) archsimd.Uint8x16 {
	return x.ReshapeToUint32s().ShiftAllRight(4).ReshapeToUint8s().And(archsimd.BroadcastUint8x16(15))
}

type decodePacker struct {
	shuffle      archsimd.Uint8x16
	pairs, low12 archsimd.Uint32x4
}

func newDecodePacker() decodePacker {
	return decodePacker{archsimd.LoadUint8x16Array(&decodePack), archsimd.BroadcastUint32x4(0x003f003f), archsimd.BroadcastUint32x4(0x00000fff)}
}
func (p decodePacker) pack(v archsimd.Uint8x16) archsimd.Uint8x16 {
	w := v.ReshapeToUint32s()
	pairs := w.And(p.pairs).ShiftAllLeft(6).Or(w.ShiftAllRight(8).And(p.pairs))
	out := pairs.And(p.low12).ShiftAllLeft(12).Or(pairs.ShiftAllRight(16))
	return lookup(out.ReshapeToUint8s(), p.shuffle)
}

type encodePacker struct {
	expand         archsimd.Uint8x16
	m0, m1, m2, m3 archsimd.Uint32x4
}

func newEncodePacker() encodePacker {
	return encodePacker{archsimd.LoadUint8x16Array(&encodeExpand), archsimd.BroadcastUint32x4(0x0000003f), archsimd.BroadcastUint32x4(0x00003f00), archsimd.BroadcastUint32x4(0x003f0000), archsimd.BroadcastUint32x4(0x3f000000)}
}
func (p encodePacker) pack(v archsimd.Uint8x16) archsimd.Uint8x16 {
	x := lookup(v, p.expand).ReshapeToUint32s()
	return x.ShiftAllRight(18).And(p.m0).Or(x.ShiftAllRight(4).And(p.m1)).Or(x.ShiftAllLeft(10).And(p.m2)).Or(x.ShiftAllLeft(24).And(p.m3)).ReshapeToUint8s()
}
