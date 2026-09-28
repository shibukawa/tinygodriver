//go:build tinygo || force_tinygo_logic

package zstd

import "testing"

// The vectors come from github.com/cespare/xxhash/v2 over the same pattern.
// The lengths straddle each tail path (4 and 8 bytes) and the 32-byte stripe.
func TestXXH64Vectors(t *testing.T) {
	p := make([]byte, 200)
	for i := range p {
		p[i] = byte(i*7 + 3)
	}
	vectors := []struct {
		n    int
		want uint64
	}{
		{0, 0xef46db3751d8e999},
		{1, 0x1f25c8d0bc1f4bb6},
		{3, 0x31d2363f52e564c9},
		{4, 0x9bb64b7d66ee9fda},
		{7, 0x9a7b149959ce60d8},
		{8, 0xdab99d95c6f90092},
		{9, 0x170bb6bf975b4c02},
		{31, 0xa2aa5f33cc4a6119},
		{32, 0x23c3c17ef790fd97},
		{33, 0x50a7cfc7ba588784},
		{63, 0x5e3e54b431c7493c},
		{64, 0x0eb64b3ef6eeb01f},
		{100, 0xa61f8d4c170fe531},
		{200, 0xa6cb3c09bc829b24},
	}
	for _, v := range vectors {
		// Whole, and fed in pieces that cut the stripes at every offset.
		for _, chunk := range []int{v.n + 1, 1, 5, 31, 33} {
			var x xxh64
			x.reset()
			for rest := p[:v.n]; len(rest) > 0; {
				k := min(chunk, len(rest))
				x.write(rest[:k])
				rest = rest[k:]
			}
			if got := x.sum64(); got != v.want {
				t.Errorf("XXH64 of %d bytes in chunks of %d = %#x, want %#x", v.n, chunk, got, v.want)
			}
		}
	}
}
