//go:build goexperiment.simd && arm64 && !tinygo && !hex_c

package hex

import "simd/archsimd"

const vectorAvailable = true

var hexAlphabet = [16]byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 'e', 'f'}

type hexEncoder struct{ alphabet, mask archsimd.Uint8x16 }

func newHexEncoder() hexEncoder {
	return hexEncoder{archsimd.LoadUint8x16Array(&hexAlphabet), archsimd.BroadcastUint8x16(15)}
}

func (e hexEncoder) store(dst *[32]byte, x archsimd.Uint8x16) {
	hi := e.alphabet.LookupOrZero(x.ShiftAllRight(4))
	lo := e.alphabet.LookupOrZero(x.And(e.mask))
	hi.InterleaveLo(lo).StoreArray((*[16]byte)(dst[:16]))
	hi.InterleaveHi(lo).StoreArray((*[16]byte)(dst[16:]))
}

func allValid(x archsimd.Uint8x16) bool { return x.ReduceMin() != 0 }

func packHex(x, y archsimd.Uint8x16) archsimd.Uint8x16 {
	return x.ConcatEven(y).ShiftAllLeft(4).Or(x.ConcatOdd(y))
}
