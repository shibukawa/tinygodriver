package bytealg

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
)

func TestHeaderValueBytes(t *testing.T) {
	for c := range 256 {
		for lane := range 80 {
			b := bytes.Repeat([]byte{'a'}, 80)
			b[lane] = byte(c)
			want := (c >= 32 && c != 127) || c == 9
			if got := ValidHeaderValue(b); got != want {
				t.Fatalf("byte %d, lane %d: got %v, want %v", c, lane, got, want)
			}
		}
	}
}

func TestBoundedKernels(t *testing.T) {
	for length := range 145 {
		for align := range 32 {
			backing := bytes.Repeat([]byte{0xa5}, align+length+32)
			b := backing[align : align+length]
			for i := range b {
				b[i] = byte(i*37 + length)
			}
			before := bytes.Clone(backing)
			for _, c := range []byte{0, 9, 32, 127, 165, 255} {
				if got, want := IndexByte(b, c), bytes.IndexByte(b, c); got != want {
					t.Fatalf("IndexByte len=%d align=%d byte=%d: %d, want %d", length, align, c, got, want)
				}
			}
			if ValidHeaderValue(b) != validHeaderScalar(b) || !bytes.Equal(backing, before) {
				t.Fatalf("scan len=%d align=%d: changed input or validation result", length, align)
			}
			key := uint32(0x7391fa02)
			n := Mask(b, key)
			wantN := 0
			if Available && length >= 64 {
				wantN = length &^ 15
			}
			if n != wantN {
				t.Fatalf("Mask len=%d: processed %d, want %d", length, n, wantN)
			}
			var k [4]byte
			binary.LittleEndian.PutUint32(k[:], key)
			for i := range n {
				before[align+i] ^= k[i&3]
			}
			if !bytes.Equal(backing, before) {
				t.Fatalf("Mask len=%d align=%d: wrong output or overwritten canary", length, align)
			}
		}
	}
	for length := range 145 {
		b := bytes.Repeat([]byte{'a'}, length+1)
		b[length] = 'x'
		if IndexByte(b[:length], 'x') != -1 {
			t.Fatalf("IndexByte read past length %d", length)
		}
		for pos := range length {
			b[pos] = 'x'
			if IndexByte(b[:length], 'x') != pos {
				t.Fatalf("IndexByte missed position %d of %d", pos, length)
			}
			b[pos] = 'a'
		}
	}
}

func BenchmarkByteKernels(b *testing.B) {
	for _, size := range []int{32, 256, 4096} {
		for _, op := range []string{"index", "header"} {
			for _, selected := range []bool{false, true} {
				b.Run(fmt.Sprintf("%s/%d/selected=%t", op, size, selected), func(b *testing.B) {
					data := bytes.Repeat([]byte{'a'}, size)
					b.SetBytes(int64(size))
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if op == "index" {
							var i int
							if selected {
								i = IndexByte(data, '<')
							} else {
								i = bytes.IndexByte(data, '<')
							}
							if i != -1 {
								b.Fatal(i)
							}
						} else {
							ok := false
							if selected {
								ok = ValidHeaderValue(data)
							} else {
								ok = validHeaderScalar(data)
							}
							if !ok {
								b.Fatal("invalid value")
							}
						}
					}
				})
			}
		}
	}
}
