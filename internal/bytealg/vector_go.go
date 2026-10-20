//go:build goexperiment.simd && (arm64 || amd64) && !tinygo && !bytealg_c

package bytealg

import "simd/archsimd"

func maskVector(b []byte, key uint32) int {
	k := archsimd.BroadcastUint32x4(key).ReshapeToUint8s()
	i := 0
	for ; i+64 <= len(b); i += 64 {
		p := (*[64]byte)(b[i:])
		archsimd.LoadUint8x16Array((*[16]byte)(p[0:16])).Xor(k).StoreArray((*[16]byte)(p[0:16]))
		archsimd.LoadUint8x16Array((*[16]byte)(p[16:32])).Xor(k).StoreArray((*[16]byte)(p[16:32]))
		archsimd.LoadUint8x16Array((*[16]byte)(p[32:48])).Xor(k).StoreArray((*[16]byte)(p[32:48]))
		archsimd.LoadUint8x16Array((*[16]byte)(p[48:64])).Xor(k).StoreArray((*[16]byte)(p[48:64]))
	}
	for ; i+16 <= len(b); i += 16 {
		p := (*[16]byte)(b[i:])
		archsimd.LoadUint8x16Array(p).Xor(k).StoreArray(p)
	}
	return i
}

func headerVector(b []byte) bool {
	space := archsimd.BroadcastUint8x16(32)
	tab := archsimd.BroadcastUint8x16('\t')
	del := archsimd.BroadcastUint8x16(127)
	one := archsimd.BroadcastUint8x16(1)
	for len(b) >= 16 {
		x := archsimd.LoadUint8x16Array((*[16]byte)(b))
		// HTAB is already in the low-control set, so XOR removes just it.
		bad := one.Masked(x.Less(space)).Xor(one.Masked(x.Equal(tab))).Or(one.Masked(x.Equal(del)))
		if anyNonzero(bad) {
			return false
		}
		b = b[16:]
	}
	return validHeaderScalar(b)
}
