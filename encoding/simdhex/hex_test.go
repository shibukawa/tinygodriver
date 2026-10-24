package simdhex

import (
	"bytes"
	std "encoding/hex"
	"fmt"
	"io"
	"strings"
	"testing"
)

func checkDecode(t *testing.T, src []byte) {
	t.Helper()
	want := bytes.Repeat([]byte{0xa5}, std.DecodedLen(len(src))+16)
	got := bytes.Clone(want)
	n, err := Decode(got[:len(got)-16], src)
	wn, we := std.Decode(want[:len(want)-16], src)
	if n != wn || err != we || !bytes.Equal(got, want) {
		t.Fatalf("Decode %x: %d/%v/%x; want %d/%v/%x", src, n, err, got, wn, we, want)
		return
	}
	str, se := DecodeString(string(src))
	if se != we || !bytes.Equal(str, want[:wn]) {
		t.Fatalf("DecodeString %x: %x/%v, want %x/%v", src, str, se, want[:wn], we)
	}
}

func TestDecodeEveryByteAndLane(t *testing.T) {
	for c := range 256 {
		for lane := range 128 {
			src := []byte(strings.Repeat("01aB9fCd", 16))
			src[lane] = byte(c)
			checkDecode(t, src)
		}
	}
	for _, text := range []string{"", "0", "f", "F", "x", "0x", "0xf", "00x", "00f", "\x10\x11", "\x1a\x1f"} {
		checkDecode(t, []byte(text))
	}
}

func TestBufferBoundsAndAppend(t *testing.T) {
	for _, size := range []int{0, 1, 2, 15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65, 127, 128, 129, 255, 256, 257, 1024, 65536} {
		for align := range 16 {
			raw := make([]byte, size)
			for i := range raw {
				raw[i] = byte(i*73 + size)
			}
			encoded := make([]byte, std.EncodedLen(size))
			std.Encode(encoded, raw)
			want := bytes.Repeat([]byte{0xa5}, align+len(encoded)+16)
			copy(want[align:], encoded)
			got := bytes.Repeat([]byte{0xa5}, len(want))
			if n := Encode(got[align:align+len(encoded)], raw); n != len(encoded) || !bytes.Equal(got, want) {
				t.Fatalf("Encode size=%d align=%d crossed its output buffer", size, align)
				return
			}
			if EncodeToString(raw) != string(encoded) {
				t.Fatal("EncodeToString differs")
				return
			}
			inplace := append(bytes.Clone(encoded), bytes.Repeat([]byte{0xa5}, 16)...)
			n, err := Decode(inplace[:size], inplace[:len(encoded)])
			if err != nil || n != size || !bytes.Equal(inplace[:n], raw) || !bytes.Equal(inplace[size:], append(encoded[size:], bytes.Repeat([]byte{0xa5}, 16)...)) {
				t.Fatalf("in-place decode size=%d: %d/%v", size, n, err)
				return
			}
			prefix := []byte("prefix:")
			appended := AppendEncode(bytes.Clone(prefix), raw)
			if !bytes.Equal(appended, std.AppendEncode(bytes.Clone(prefix), raw)) {
				t.Fatal("AppendEncode differs")
				return
			}
			decoded, err := AppendDecode(bytes.Clone(prefix), encoded)
			if err != nil || !bytes.Equal(decoded, append(bytes.Clone(prefix), raw...)) {
				t.Fatal("AppendDecode differs")
				return
			}
			if size <= 257 {
				checkDecode(t, append(bytes.Clone(encoded), 'f'))
				checkDecode(t, append(bytes.Clone(encoded), 'x'))
				partial, pe := AppendDecode(bytes.Clone(prefix), append(bytes.Clone(encoded), 'x'))
				wp, we := std.AppendDecode(bytes.Clone(prefix), append(bytes.Clone(encoded), 'x'))
				if pe != we || !bytes.Equal(partial, wp) {
					t.Fatal("AppendDecode lost partial output")
					return
				}
			}
		}
	}
}

func TestStreamingAdapters(t *testing.T) {
	raw := bytes.Repeat([]byte("hex adapter\x00\xff"), 200)
	var out bytes.Buffer
	w := NewEncoder(&out)
	for start := 0; start < len(raw); start += 17 {
		if _, err := w.Write(raw[start:min(start+17, len(raw))]); err != nil {
			t.Fatal(err)
			return
		}
	}
	if out.String() != std.EncodeToString(raw) {
		t.Fatal("stream encoder differs")
		return
	}
	decoded, err := io.ReadAll(NewDecoder(&out))
	if err != nil || !bytes.Equal(decoded, raw) {
		t.Fatalf("stream decode: %v", err)
		return
	}
	if Dump(raw) != std.Dump(raw) {
		t.Fatal("dump differs")
	}
}

func BenchmarkHex(b *testing.B) {
	for _, size := range []int{16, 32, 64, 1024, 65536} {
		raw := make([]byte, size)
		for i := range raw {
			raw[i] = byte(i*73 + size)
		}
		encoded := []byte(std.EncodeToString(raw))
		for _, selected := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/Encode/selected=%t", size, selected), func(b *testing.B) {
				dst := make([]byte, len(encoded))
				fn := std.Encode
				if selected {
					fn = Encode
				}
				if fn(dst, raw) != len(encoded) || !bytes.Equal(dst, encoded) {
					b.Fatal("invalid encoding")
					return
				}
				b.SetBytes(int64(size))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if fn(dst, raw) != len(encoded) {
						b.Fatal("short encoding")
						return
					}
				}
			})
			b.Run(fmt.Sprintf("%d/Decode/selected=%t", size, selected), func(b *testing.B) {
				dst := make([]byte, size)
				fn := std.Decode
				if selected {
					fn = Decode
				}
				if n, err := fn(dst, encoded); err != nil || n != size || !bytes.Equal(dst, raw) {
					b.Fatal("invalid decoding")
					return
				}
				b.SetBytes(int64(size))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if n, err := fn(dst, encoded); err != nil || n != size {
						b.Fatal("invalid decoding")
						return
					}
				}
			})
		}
	}
}
