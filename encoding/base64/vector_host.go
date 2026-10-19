//go:build goexperiment.simd && (arm64 || amd64) && !tinygo && !base64_c

package base64

import "simd/archsimd"

// The low-nibble table contains a bit for each permitted high nibble. AND
// with the high-nibble table both validates ASCII and rejects high-bit bytes.
var highBits = [16]byte{0, 0, 1, 2, 4, 8, 16, 32}
var lowBits = [2][16]byte{
	{42, 62, 62, 62, 62, 62, 62, 62, 62, 62, 60, 21, 20, 20, 20, 21},
	{42, 62, 62, 62, 62, 62, 62, 62, 62, 62, 60, 20, 20, 21, 20, 28},
}
var decodeOffsets = [2][16]byte{
	{0, 0, 19, 4, 191, 191, 185, 185},
	{0, 0, 17, 4, 191, 191, 185, 185},
}
var decodePack = [16]byte{2, 1, 0, 6, 5, 4, 10, 9, 8, 14, 13, 12, 255, 255, 255, 255}
var encodeExpand = [16]byte{2, 1, 0, 255, 5, 4, 3, 255, 8, 7, 6, 255, 11, 10, 9, 255}
var encodeOffsets = [2][16]byte{
	{65, 71, 252, 252, 252, 252, 252, 252, 252, 252, 252, 252, 237, 240},
	{65, 71, 252, 252, 252, 252, 252, 252, 252, 252, 252, 252, 239, 32},
}

// decodeVector consumes complete, valid, unpadded quanta and CR/LF at quantum
// boundaries, returning separate source and output counts. A full vector
// store produces 12 useful bytes and four scratch bytes inside dst's length.
func decodeVector(dst, src []byte, url bool) (read, written int) {
	k := 0
	if url {
		k = 1
	}
	hi := archsimd.LoadUint8x16Array(&highBits)
	lo := archsimd.LoadUint8x16Array(&lowBits[k])
	delta := archsimd.LoadUint8x16Array(&decodeOffsets[k])
	mask15 := archsimd.BroadcastUint8x16(15)
	packer := newDecodePacker()
	special := byte('/')
	correction := byte(253) // -3, for '/'
	if url {
		special, correction = '_', 33
	}
	specialV := archsimd.BroadcastUint8x16(special)
	correctionV := archsimd.BroadcastUint8x16(correction)
	tail4 := archsimd.LoadUint8x16Array(&[16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1})
	tail8 := archsimd.LoadUint8x16Array(&[16]byte{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1})
	tail12 := archsimd.LoadUint8x16Array(&[16]byte{0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1})
	for len(src)-read >= 16 && len(dst) >= 16 {
		x := archsimd.LoadUint8x16(src[read:])
		h := highNibbles(x)
		valid := lookup(lo, x.And(mask15)).And(lookup(hi, h))
		digits := 16
		if !allNonzero(valid) {
			// Whitespace is uncommon in the clean-data loop. Only inspect
			// scalar characters when the vector actually rejected a block.
			if src[read] == '\r' || src[read] == '\n' {
				for read < len(src) && (src[read] == '\r' || src[read] == '\n') {
					read++
				}
				continue
			}
			switch {
			case allNonzero(valid.Or(tail4)):
				digits = 12
			case allNonzero(valid.Or(tail8)):
				digits = 8
			case allNonzero(valid.Or(tail12)):
				digits = 4
			default:
				return read, written
			}
		}
		v := x.Add(lookup(delta, h))
		v = v.Add(correctionV.Masked(x.Equal(specialV)))
		packed := packer.pack(v)
		if digits == 16 {
			packed.Store(dst)
			read += 16
			written += 12
			dst = dst[12:]
			continue
		}
		produced := digits / 4 * 3
		// Do not overwrite unread source when decoding in place at a
		// short fringe (for example twelve digits followed by CR/LF).
		packed.StorePart(dst[:produced])
		read += digits
		written += produced
		dst = dst[produced:]
	}
	return read, written
}

// encodeVector reads 16 bytes but consumes 12. It leaves the last 12..15
// bytes to the scalar encoder, avoiding any out-of-bounds load.
func encodeVector(dst, src []byte, url bool) int {
	k := 0
	if url {
		k = 1
	}
	offsets := archsimd.LoadUint8x16Array(&encodeOffsets[k])
	packer := newEncodePacker()
	n := 0
	for len(src)-n >= 16 && len(dst) >= 16 {
		v := packer.pack(archsimd.LoadUint8x16(src[n:]))
		index := v.SubSaturated(archsimd.BroadcastUint8x16(51))
		index = index.Add(archsimd.BroadcastUint8x16(1).Masked(v.BitsToInt8().Greater(archsimd.BroadcastInt8x16(25))))
		v.Add(lookup(offsets, index)).Store(dst)
		n += 12
		dst = dst[16:]
	}
	return n
}
