//go:build goexperiment.simd && amd64 && !tinygo && !bytealg_c

package bytealg

import "simd/archsimd"

var Available = archsimd.X86.AVX2()

func anyNonzero(x archsimd.Uint8x16) bool {
	return x.Equal(archsimd.BroadcastUint8x16(0)).ToBits() != 0xffff
}
