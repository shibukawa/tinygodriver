//go:build tinygo || force_tinygo_logic

// XXH64, which the format's Content_Checksum is taken from: the low 32 bits of
// the digest of the frame's content, with seed 0.
//
// The encoder never writes a checksum, but the reference CLI does by default,
// so a decoder that means to verify one needs the hash. It is kept streaming,
// fed a block at a time, so that a decoder need not hold a whole frame's
// content to check it.

package zstd

import (
	"encoding/binary"
	"math/bits"
)

const (
	xxPrime1 uint64 = 0x9e3779b185ebca87
	xxPrime2 uint64 = 0xc2b2ae3d27d4eb4f
	xxPrime3 uint64 = 0x165667b19e3779f9
	xxPrime4 uint64 = 0x85ebca77c2b2ae63
	xxPrime5 uint64 = 0x27d4eb2f165667c5
)

// xxh64 is a running XXH64 digest with seed 0. The zero value is not ready;
// call reset first.
type xxh64 struct {
	v     [4]uint64
	total uint64
	mem   [32]byte
	n     int // bytes buffered in mem
}

func (x *xxh64) reset() {
	// The first lane starts at seed + prime1 + prime2 and the fourth at
	// seed - prime1. Both wrap, which a constant expression may not, so they
	// are spelled out.
	x.v = [4]uint64{0x60ea27eeadc0b5d6, xxPrime2, 0, 0x61c8864e7a143579}
	x.total = 0
	x.n = 0
}

func xxRound(acc, input uint64) uint64 {
	acc += input * xxPrime2
	return bits.RotateLeft64(acc, 31) * xxPrime1
}

func (x *xxh64) write(p []byte) {
	x.total += uint64(len(p))
	if x.n > 0 {
		c := copy(x.mem[x.n:], p)
		x.n += c
		p = p[c:]
		if x.n < 32 {
			return
		}
		x.stripes(x.mem[:])
		x.n = 0
	}
	whole := len(p) &^ 31
	x.stripes(p[:whole])
	x.n = copy(x.mem[:], p[whole:])
}

// stripes consumes p, a multiple of 32 bytes, one 8-byte lane per accumulator.
func (x *xxh64) stripes(p []byte) {
	v0, v1, v2, v3 := x.v[0], x.v[1], x.v[2], x.v[3]
	for ; len(p) >= 32; p = p[32:] {
		v0 = xxRound(v0, binary.LittleEndian.Uint64(p))
		v1 = xxRound(v1, binary.LittleEndian.Uint64(p[8:]))
		v2 = xxRound(v2, binary.LittleEndian.Uint64(p[16:]))
		v3 = xxRound(v3, binary.LittleEndian.Uint64(p[24:]))
	}
	x.v = [4]uint64{v0, v1, v2, v3}
}

func (x *xxh64) sum64() uint64 {
	var h uint64
	if x.total >= 32 {
		v := x.v
		h = bits.RotateLeft64(v[0], 1) + bits.RotateLeft64(v[1], 7) +
			bits.RotateLeft64(v[2], 12) + bits.RotateLeft64(v[3], 18)
		for _, lane := range v {
			h ^= xxRound(0, lane)
			h = h*xxPrime1 + xxPrime4
		}
	} else {
		h = xxPrime5
	}
	h += x.total

	p := x.mem[:x.n]
	for ; len(p) >= 8; p = p[8:] {
		h ^= xxRound(0, binary.LittleEndian.Uint64(p))
		h = bits.RotateLeft64(h, 27)*xxPrime1 + xxPrime4
	}
	if len(p) >= 4 {
		h ^= uint64(binary.LittleEndian.Uint32(p)) * xxPrime1
		h = bits.RotateLeft64(h, 23)*xxPrime2 + xxPrime3
		p = p[4:]
	}
	for _, b := range p {
		h ^= uint64(b) * xxPrime5
		h = bits.RotateLeft64(h, 11) * xxPrime1
	}

	h ^= h >> 33
	h *= xxPrime2
	h ^= h >> 29
	h *= xxPrime3
	h ^= h >> 32
	return h
}
