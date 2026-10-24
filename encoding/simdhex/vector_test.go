package simdhex

import (
	"bytes"
	std "encoding/hex"
	"testing"
)

// TestVectorKernels checks the selected kernel's contract directly: it
// consumes whole blocks bounded by both slice lengths, stops before the first
// invalid block and writes nothing past the bytes it reports.
func TestVectorKernels(t *testing.T) {
	if !vectorAvailable {
		// TinyGo's WASI test runner cannot implement SkipNow/Goexit. The
		// compatibility tests still exercise the selected scalar backend.
		t.Log("SIMD backend unavailable; scalar compatibility tests apply")
		return
	}
	raw := make([]byte, 160)
	for i := range raw {
		raw[i] = byte(i*73 + 5)
	}
	encoded := []byte(std.EncodeToString(raw))
	canary := func(n int) []byte { return bytes.Repeat([]byte{0xa5}, n) }
	for _, srcLen := range []int{0, 15, 16, 17, 31, 32, 33, 47, 48, 64, 80, 160} {
		for _, dstLen := range []int{0, 31, 32, 33, 63, 64, 65, 95, 96, 160, 320} {
			want := min(srcLen/16, dstLen/32) * 16
			dst := canary(dstLen + 16)
			n := encodeVector(dst[:dstLen], raw[:srcLen])
			if n != want || !bytes.Equal(dst[:2*n], encoded[:2*n]) || !bytes.Equal(dst[2*n:], canary(len(dst)-2*n)) {
				t.Fatalf("encodeVector(dst[:%d], src[:%d]) = %d, want %d; dst %x", dstLen, srcLen, n, want, dst)
				return
			}
		}
	}
	for _, srcLen := range []int{0, 31, 32, 33, 63, 64, 65, 95, 96, 160, 320} {
		for _, dstLen := range []int{0, 15, 16, 17, 31, 32, 33, 48, 80, 160} {
			want := min(srcLen/32, dstLen/16) * 32
			dst := canary(dstLen + 16)
			n := decodeVector(dst[:dstLen], encoded[:srcLen])
			if n != want || !bytes.Equal(dst[:n/2], raw[:n/2]) || !bytes.Equal(dst[n/2:], canary(len(dst)-n/2)) {
				t.Fatalf("decodeVector(dst[:%d], src[:%d]) = %d, want %d; dst %x", dstLen, srcLen, n, want, dst)
				return
			}
		}
	}
	for lane := range 96 {
		for _, c := range []byte{'x', 'g', 'G', '/', ':', '@', '`', 0, 0xff} {
			bad := bytes.Clone(encoded[:96])
			bad[lane] = c
			dst := canary(64)
			n := decodeVector(dst[:48], bad)
			if want := lane / 32 * 32; n != want || !bytes.Equal(dst[:n/2], raw[:n/2]) || !bytes.Equal(dst[n/2:], canary(len(dst)-n/2)) {
				t.Fatalf("decodeVector with %q at %d = %d, want %d", c, lane, n, want)
				return
			}
		}
	}
	upper := bytes.ToUpper(encoded[:64])
	dst := canary(32)
	if n := decodeVector(dst, upper); n != 64 || !bytes.Equal(dst, raw[:32]) {
		t.Fatalf("uppercase decodeVector = %d, dst %x", n, dst)
	}
}
