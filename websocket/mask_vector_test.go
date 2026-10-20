package websocket

import (
	"bytes"
	"testing"
)

func TestMaskVectorBoundaries(t *testing.T) {
	key := [4]byte{0x12, 0x73, 0xfa, 0x91}
	for _, size := range []int{0, 1, 15, 16, 17, 63, 64, 65, 127, 128, 129, 1024, 65536} {
		for align := range 32 {
			for pos := range 4 {
				backing := bytes.Repeat([]byte{0xa5}, align+size+32)
				data := backing[align : align+size]
				for i := range data {
					data[i] = byte(i*37 + size)
				}
				want := bytes.Clone(backing)
				wantPos := maskBytesByByte(key, pos, want[align:align+size])
				gotPos := maskBytes(key, pos, data)
				if gotPos != wantPos || !bytes.Equal(backing, want) {
					t.Fatalf("size=%d align=%d pos=%d: wrong bytes, tail position or canary", size, align, pos)
					return
				}
				// Split a second application across a non-key-aligned boundary.
				split := min(size, 65)
				phase := maskBytes(key, pos, data[:split])
				maskBytes(key, phase, data[split:])
				for i, c := range data {
					if c != byte(i*37+size) {
						t.Fatal("split mask lost key position")
						return
					}
				}
			}
		}
	}
}
