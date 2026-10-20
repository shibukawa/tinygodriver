//go:build tinygo || force_tinygo_logic

package zstd

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Compare refill windows against a bit-at-a-time reader, including every end
// marker position, short tails, zero-width fields and reads past the marker.
func TestBackReaderBitOracle(t *testing.T) {
	for length := 1; length <= 96; length++ {
		for marker := range 8 {
			in := make([]byte, length)
			for i := range in {
				in[i] = byte(i*73 + length)
			}
			in[length-1] = byte(1<<marker) | in[length-1]&byte((1<<marker)-1)
			available := (length-1)*8 + marker
			var r backReader
			if err := r.init(in); err != nil {
				t.Fatal(err)
				return
			}
			read := 0
			for step := 0; read <= available+32; step++ {
				n := (step*13 + length) % 33
				r.fill()
				if r.finished() != (read == available) || r.overread() != (read > available) {
					t.Fatalf("length=%d marker=%d read=%d: incorrect end state", length, marker, read)
					return
				}
				var want uint32
				for bit := range n {
					want <<= 1
					pos := available - 1 - read - bit
					if pos >= 0 {
						want |= uint32(in[pos/8] >> uint(pos%8) & 1)
					}
				}
				if got := r.bits(uint8(n)); got != want {
					t.Fatalf("length=%d marker=%d read=%d width=%d: %x, want %x", length, marker, read, n, got, want)
					return
				}
				read += n
			}
		}
	}
}

func TestHuffmanInterleavedStreams(t *testing.T) {
	for _, width := range []uint8{2, 8} {
		var enc huffTable
		var dec huffDecTable
		if width == 8 {
			enc.max, enc.maxBit = 255, 8
			for i := range 256 {
				enc.bits[i], enc.vals[i] = 8, uint16(i)
			}
			if err := dec.build(bytes.Repeat([]byte{1}, 255)); err != nil {
				t.Fatal(err)
				return
			}
		} else {
			enc.max, enc.maxBit = 2, 2
			enc.bits[0], enc.vals[0] = 1, 1
			enc.bits[1], enc.vals[1] = 2, 0
			enc.bits[2], enc.vals[2] = 2, 1
			if err := dec.build([]byte{2, 1}); err != nil {
				t.Fatal(err)
				return
			}
		}
		for _, length := range []int{6, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 1023, 1024, 1025, 8193} {
			plain := make([]byte, length)
			for i := range plain {
				plain[i] = byte((i*73 + length) % (enc.max + 1))
			}
			segment := (length + 3) / 4
			body := make([]byte, 6)
			for stream := range 4 {
				lo, hi := stream*segment, min((stream+1)*segment, length)
				start := len(body)
				body = appendHuffStream(body, plain[lo:hi], &enc)
				if stream < 3 {
					binary.LittleEndian.PutUint16(body[2*stream:], uint16(len(body)-start))
				}
			}
			guard := bytes.Repeat([]byte{0xa5}, length+32)
			out := guard[16 : 16+length]
			if err := dec.decode4(out, body); err != nil || !bytes.Equal(out, plain) {
				t.Fatalf("width=%d length=%d: %v, incorrect output", width, length, err)
				return
			}
			if !bytes.Equal(guard[:16], bytes.Repeat([]byte{0xa5}, 16)) || !bytes.Equal(guard[16+length:], bytes.Repeat([]byte{0xa5}, 16)) {
				t.Fatal("Huffman output crossed its buffer")
				return
			}
			// A byte-aligned code stream always ends with a separate marker
			// byte. Clearing any stream's marker must reject it.
			if width == 8 {
				end := 6
				for stream := range 4 {
					size := len(body) - end
					if stream < 3 {
						size = int(binary.LittleEndian.Uint16(body[2*stream:]))
					}
					end += size
					bad := bytes.Clone(body)
					bad[end-1] = 0
					if err := dec.decode4(out, bad); err == nil {
						t.Fatal("accepted missing end marker")
						return
					}
				}
			}
			for cut := 0; cut < min(len(body), 32); cut++ {
				if err := dec.decode4(out, body[:cut]); err == nil {
					t.Fatal("accepted truncated streams")
					return
				}
			}
		}
	}
}
