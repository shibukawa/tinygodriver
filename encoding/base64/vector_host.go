//go:build goexperiment.simd && (arm64 || amd64) && !tinygo && !base64_c

package base64

import "simd/archsimd"

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
	hi := archsimd.LoadUint8x16Array(&highBits[k])
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
	for len(src)-read >= 64 && len(dst) >= 52 {
		remaining := src[read:]
		if remaining[0] == '\r' || remaining[0] == '\n' {
			for read < len(src) && (src[read] == '\r' || src[read] == '\n') {
				read++
			}
			continue
		}
		// MIME lines contain 76 digits, followed by LF or CR/LF. Validate
		// the whole line before writing, including the twelve-digit fringe.
		// The fifth load may read the separator/next line, but those four
		// lanes are excluded from validation and from the bounded store.
		if len(remaining) >= 80 && len(dst) >= 57 && (remaining[76] == '\n' || remaining[76] == '\r') {
			block := (*[80]byte)(remaining)
			out := (*[57]byte)(dst)
			x0 := archsimd.LoadUint8x16(block[0:16])
			x1 := archsimd.LoadUint8x16(block[16:32])
			x2 := archsimd.LoadUint8x16(block[32:48])
			x3 := archsimd.LoadUint8x16(block[48:64])
			x4 := archsimd.LoadUint8x16(block[64:80])
			h0, v0 := validateVector(x0, lo, hi, mask15)
			h1, v1 := validateVector(x1, lo, hi, mask15)
			h2, v2 := validateVector(x2, lo, hi, mask15)
			h3, v3 := validateVector(x3, lo, hi, mask15)
			h4, v4 := validateVector(x4, lo, hi, mask15)
			if allNonzero(v0.Min(v1).Min(v2.Min(v3)).Min(v4.Or(tail4))) {
				p0 := packer.pack(translateVector(x0, h0, delta, specialV, correctionV))
				p1 := packer.pack(translateVector(x1, h1, delta, specialV, correctionV))
				p2 := packer.pack(translateVector(x2, h2, delta, specialV, correctionV))
				p3 := packer.pack(translateVector(x3, h3, delta, specialV, correctionV))
				p4 := packer.pack(translateVector(x4, h4, delta, specialV, correctionV))
				p0.Store(out[0:16])
				p1.Store(out[12:28])
				p2.Store(out[24:40])
				p3.Store(out[36:52])
				p4.StorePart(out[48:57])
				separator := block[76]
				read += 77
				if separator == '\r' && block[77] == '\n' {
					read++
				}
				written += 57
				dst = dst[57:]
				continue
			}
		}
		// Four independent vectors share one scalar validity decision. No
		// stores precede that decision: a rejected batch is retried below,
		// preserving the standard library's decoded prefix and error offset.
		block := (*[64]byte)(remaining)
		out := (*[52]byte)(dst)
		x0 := archsimd.LoadUint8x16(block[0:16])
		x1 := archsimd.LoadUint8x16(block[16:32])
		x2 := archsimd.LoadUint8x16(block[32:48])
		x3 := archsimd.LoadUint8x16(block[48:64])
		h0, v0 := validateVector(x0, lo, hi, mask15)
		h1, v1 := validateVector(x1, lo, hi, mask15)
		h2, v2 := validateVector(x2, lo, hi, mask15)
		h3, v3 := validateVector(x3, lo, hi, mask15)
		if allNonzero(v0.Min(v1).Min(v2.Min(v3))) {
			p0 := packer.pack(translateVector(x0, h0, delta, specialV, correctionV))
			p1 := packer.pack(translateVector(x1, h1, delta, specialV, correctionV))
			p2 := packer.pack(translateVector(x2, h2, delta, specialV, correctionV))
			p3 := packer.pack(translateVector(x3, h3, delta, specialV, correctionV))
			p0.Store(out[0:16])
			p1.Store(out[12:28])
			p2.Store(out[24:40])
			p3.Store(out[36:52])
			read += 64
			written += 48
			dst = dst[48:]
			continue
		}
		// Stay in the small loop after a rejected batch. Short lines then
		// pay neither repeated bulk validation nor bulk-loop bookkeeping.
		break
	}
	for len(src)-read >= 16 && len(dst) >= 16 {
		x := archsimd.LoadUint8x16(src[read:])
		h, valid := validateVector(x, lo, hi, mask15)
		digits := 16
		if !allNonzero(valid) {
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
		v := translateVector(x, h, delta, specialV, correctionV)
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
	mask63 := archsimd.BroadcastUint8x16(63)
	v51 := archsimd.BroadcastUint8x16(51)
	one := archsimd.BroadcastUint8x16(1)
	v25 := archsimd.BroadcastInt8x16(25)
	n := 0
	for len(src)-n >= 52 && len(dst) >= 64 {
		block := (*[52]byte)(src[n:])
		out := (*[64]byte)(dst)
		v0 := packer.pack(archsimd.LoadUint8x16(block[0:16]), mask63)
		v1 := packer.pack(archsimd.LoadUint8x16(block[12:28]), mask63)
		v2 := packer.pack(archsimd.LoadUint8x16(block[24:40]), mask63)
		v3 := packer.pack(archsimd.LoadUint8x16(block[36:52]), mask63)
		i0 := v0.SubSaturated(v51).Add(one.Masked(v0.BitsToInt8().Greater(v25)))
		i1 := v1.SubSaturated(v51).Add(one.Masked(v1.BitsToInt8().Greater(v25)))
		i2 := v2.SubSaturated(v51).Add(one.Masked(v2.BitsToInt8().Greater(v25)))
		i3 := v3.SubSaturated(v51).Add(one.Masked(v3.BitsToInt8().Greater(v25)))
		v0.Add(lookup(offsets, i0)).Store(out[0:16])
		v1.Add(lookup(offsets, i1)).Store(out[16:32])
		v2.Add(lookup(offsets, i2)).Store(out[32:48])
		v3.Add(lookup(offsets, i3)).Store(out[48:64])
		n += 48
		dst = dst[64:]
	}
	for len(src)-n >= 16 && len(dst) >= 16 {
		v := packer.pack(archsimd.LoadUint8x16(src[n:]), mask63)
		index := v.SubSaturated(v51)
		index = index.Add(one.Masked(v.BitsToInt8().Greater(v25)))
		v.Add(lookup(offsets, index)).Store(dst)
		n += 12
		dst = dst[16:]
	}
	return n
}
