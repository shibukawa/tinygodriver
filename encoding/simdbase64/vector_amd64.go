//go:build goexperiment.simd && amd64 && !tinygo && !base64_c

package simdbase64

import "simd/archsimd"

// Guard every entry to the AVX kernel, including on GOAMD64=v1 builds.
var vectorAvailable = archsimd.X86.AVX2()

var highBits = [2][16]byte{
	{0, 0, 1, 2, 4, 8, 16, 32},
	{0, 0, 1, 2, 4, 8, 16, 32},
}
var lowBits = [2][16]byte{
	{42, 62, 62, 62, 62, 62, 62, 62, 62, 62, 60, 21, 20, 20, 20, 21},
	{42, 62, 62, 62, 62, 62, 62, 62, 62, 62, 60, 20, 20, 21, 20, 28},
}
var decodeOffsets = [2][16]byte{
	{0, 0, 19, 4, 191, 191, 185, 185},
	{0, 0, 17, 4, 191, 191, 185, 185},
}

func validateVector(x, lo, hi, mask archsimd.Uint8x16) (archsimd.Uint8x16, archsimd.Uint8x16) {
	h := highNibbles(x)
	return h, lookup(lo, x.And(mask)).And(lookup(hi, h))
}

func translateVector(x, h, delta, special, correction archsimd.Uint8x16) archsimd.Uint8x16 {
	return x.Add(lookup(delta, h)).Add(correction.Masked(x.Equal(special)))
}

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
	expand archsimd.Uint8x16
	m1, m2 archsimd.Uint32x4
}

func newEncodePacker() encodePacker {
	return encodePacker{archsimd.LoadUint8x16Array(&encodeExpand), archsimd.BroadcastUint32x4(0x0000ff00), archsimd.BroadcastUint32x4(0x00ff0000)}
}
func (p encodePacker) pack(v, mask archsimd.Uint8x16) archsimd.Uint8x16 {
	x := lookup(v, p.expand).ReshapeToUint32s()
	// The shuffle clears each word's high byte, so x>>18 already fits six
	// bits. Masking once at the end keeps this receiver below four vectors.
	return x.ShiftAllRight(18).Or(x.ShiftAllRight(4).And(p.m1)).Or(x.ShiftAllLeft(10).And(p.m2)).Or(x.ShiftAllLeft(24)).ReshapeToUint8s().And(mask)
}
