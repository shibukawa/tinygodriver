package base64

import (
	"bytes"
	std "encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"
)

type encodingPair struct {
	name string
	ours *Encoding
	std  *std.Encoding
}

func encodings() []encodingPair {
	return []encodingPair{
		{"std", StdEncoding, std.StdEncoding},
		{"url", URLEncoding, std.URLEncoding},
		{"raw", RawStdEncoding, std.RawStdEncoding},
		{"rawURL", RawURLEncoding, std.RawURLEncoding},
		{"strict", StdEncoding.Strict(), std.StdEncoding.Strict()},
		{"rawStrict", RawURLEncoding.Strict(), std.RawURLEncoding.Strict()},
		{"padding", StdEncoding.WithPadding('.'), std.StdEncoding.WithPadding('.')},
		{"highPadding", URLEncoding.WithPadding(0xff).Strict(), std.URLEncoding.WithPadding(0xff).Strict()},
		{"custom", NewEncoding("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz+/"), std.NewEncoding("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz+/")},
	}
}

func randomBytes(n int) []byte {
	raw := make([]byte, n)
	x := uint32(123456789)
	for i := range raw {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		raw[i] = byte(x)
	}
	return raw
}

func checkDecode(t *testing.T, pair encodingPair, src []byte) {
	t.Helper()
	want, wantErr := pair.std.DecodeString(string(src))
	got, gotErr := pair.ours.DecodeString(string(src))
	if !bytes.Equal(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatalf("%s decode %q: got %x, %v; want %x, %v", pair.name, src, got, gotErr, want, wantErr)
	}
	if wantErr != nil {
		if _, ok := gotErr.(CorruptInputError); !ok {
			t.Fatalf("wrong error type %T", gotErr)
		}
	}
	inPlace := bytes.Clone(src)
	n, err := pair.ours.Decode(inPlace, inPlace)
	if !bytes.Equal(inPlace[:n], want) || fmt.Sprint(err) != fmt.Sprint(wantErr) {
		t.Fatalf("%s in-place decode mismatch", pair.name)
	}
	// A buffer whose length ends immediately before the canary catches SIMD
	// stores past the caller's slice, including into spare slice capacity.
	for _, extra := range []int{0, 1, 4, 16} {
		size := pair.std.DecodedLen(len(src)) + extra
		backing := bytes.Repeat([]byte{0xa5}, size+16)
		n, err := pair.ours.Decode(backing[:size], src)
		if !bytes.Equal(backing[:n], want) || fmt.Sprint(err) != fmt.Sprint(wantErr) {
			t.Fatalf("%s Decode mismatch", pair.name)
		}
		if !bytes.Equal(backing[size:], bytes.Repeat([]byte{0xa5}, 16)) {
			t.Fatal("decode wrote beyond dst length")
		}
	}
}

func TestRoundTripCompatibility(t *testing.T) {
	for _, pair := range encodings() {
		t.Run(pair.name, func(t *testing.T) {
			for size := 0; size <= 300; size++ {
				raw := randomBytes(size)
				want := pair.std.EncodeToString(raw)
				if got := pair.ours.EncodeToString(raw); got != want {
					t.Fatalf("encode size %d: %q != %q", size, got, want)
				}
				backing := bytes.Repeat([]byte{0xa5}, len(want)+16)
				pair.ours.Encode(backing[:len(want)], raw)
				if string(backing[:len(want)]) != want || !bytes.Equal(backing[len(want):], bytes.Repeat([]byte{0xa5}, 16)) {
					t.Fatalf("encode bounds/output size %d", size)
				}
				checkDecode(t, pair, []byte(want))
				// Shift CR/LF across every SIMD and quantum boundary, including
				// line lengths not divisible by four and CR/LF inside padding.
				if size%13 == 0 {
					for _, width := range []int{1, 3, 15, 16, 17, 76} {
						var folded strings.Builder
						for start := 0; start < len(want); start += width {
							end := min(start+width, len(want))
							folded.WriteString(want[start:end])
							folded.WriteString("\r\n")
						}
						checkDecode(t, pair, []byte(folded.String()))
					}
				}
			}
			for _, size := range []int{1023, 1024, 1025, 65536} {
				raw := randomBytes(size)
				want := pair.std.EncodeToString(raw)
				if pair.ours.EncodeToString(raw) != want {
					t.Fatal("large encode mismatch")
				}
				checkDecode(t, pair, []byte(want))
			}
		})
	}
}

func TestEveryByteInEveryVectorLane(t *testing.T) {
	for _, pair := range encodings() {
		if pair.ours.kind == 0 {
			continue
		}
		// Reach the C backend's bulk threshold as well as Go's, so every
		// byte is checked by the new classifier before any fallback.
		original := []byte(pair.std.EncodeToString(randomBytes(192)))
		for lane := 0; lane < 16; lane++ {
			for c := 0; c < 256; c++ {
				src := bytes.Clone(original)
				src[32+lane] = byte(c)
				want, wantErr := pair.std.DecodeString(string(src))
				got, gotErr := pair.ours.DecodeString(string(src))
				if !bytes.Equal(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
					t.Fatalf("%s lane=%d byte=%02x: got %x %v want %x %v", pair.name, lane, c, got, gotErr, want, wantErr)
				}
			}
		}
	}
}

// Batch rejection must preserve valid output before an error in any of the
// four bulk vectors or the fifth MIME fringe vector. The last four lanes of
// the MIME load belong to the separator/next line and must be retried later.
func TestBatchDecodeCompatibility(t *testing.T) {
	for _, pair := range encodings() {
		if pair.ours.kind == 0 {
			continue
		}
		plain := pair.std.EncodeToString(randomBytes(384))
		for _, separator := range []string{"", "\n", "\r\n", "\r"} {
			src := []byte(plain)
			start := 64
			if separator != "" {
				src = nil
				for i := 0; i < len(plain); i += 76 {
					src = append(src, plain[i:min(i+76, len(plain))]...)
					src = append(src, separator...)
				}
				start = 76 + len(separator)
			}
			checkDecode(t, pair, src)
			for lane := 0; lane < 80; lane++ {
				for _, c := range []byte{0, '!', ' ', '=', 0x80, 0xff, '\r', '\n'} {
					mutated := bytes.Clone(src)
					mutated[start+lane] = c
					checkDecode(t, pair, mutated)
				}
			}
		}
	}
}

func TestBatchBufferEdges(t *testing.T) {
	if !vectorAvailable {
		t.Log("SIMD backend unavailable; scalar compatibility tests apply")
		return
	}
	lengths := []int{255, 256, 257, 319, 320, 321}
	for n := 63; n <= 81; n++ {
		lengths = append(lengths, n)
	}
	for _, pair := range encodings()[:4] {
		plain := []byte(pair.std.EncodeToString(randomBytes(256)))
		mime := append(bytes.Clone(plain[:76]), '\r', '\n')
		mime = append(mime, plain[76:]...)
		for _, input := range [][]byte{plain, mime} {
			for offset := 0; offset < 16; offset++ {
				for _, slen := range lengths {
					for dlen := 47; dlen <= 61; dlen++ {
						src := append(make([]byte, offset), input[:slen]...)[offset:]
						before := bytes.Clone(src)
						backing := bytes.Repeat([]byte{0xa5}, offset+dlen+16)
						consumed, produced := decodeVector(backing[offset:offset+dlen], src, pair.ours.kind == 2)
						if consumed < 0 || consumed > slen || produced < 0 || produced > dlen {
							t.Fatal("invalid kernel counts", consumed, produced)
						}
						want, err := pair.std.DecodeString(string(src[:consumed]))
						if err != nil || !bytes.Equal(backing[offset:offset+produced], want) {
							t.Fatal("batch fringe mismatch", pair.name, offset, slen, dlen, err)
						}
						if !bytes.Equal(backing[:offset], bytes.Repeat([]byte{0xa5}, offset)) ||
							!bytes.Equal(backing[offset+dlen:], bytes.Repeat([]byte{0xa5}, 16)) || !bytes.Equal(src, before) {
							t.Fatal("batch kernel wrote outside dst or modified src")
						}
					}
				}
			}
		}
	}
}

func TestMalformedSuffixes(t *testing.T) {
	for _, pair := range encodings() {
		prefix := pair.std.EncodeToString(randomBytes(60))
		for _, suffix := range []string{"", "A", "AA", "AAA", "AAAA", "AA=", "AA==", "AB==", "AAB=", "AAA=", "AA===", "====", "A===", "=AAA", "AA==A", "AA==\nA", "AA=\r\n=\n", "AA==\xff", "AA== ", "AA\tAA", "AA AA", "AA\vAA", "AA\fAA", "\r\n\r\n", "\r\nA", "\rA\nA\r", "\xff", "\n\xff"} {
			checkDecode(t, pair, []byte(prefix+suffix))
		}
	}
}

func TestAppendAndStreamCompatibility(t *testing.T) {
	for _, pair := range encodings() {
		raw := randomBytes(1024)
		prefix := []byte("prefix:")
		got := pair.ours.AppendEncode(bytes.Clone(prefix), raw)
		want := pair.std.AppendEncode(bytes.Clone(prefix), raw)
		if !bytes.Equal(got, want) {
			t.Fatal("AppendEncode mismatch")
		}
		for _, suffix := range []string{"", "\r\n", "A", "!!!!"} {
			src := append(bytes.Clone(want[len(prefix):]), suffix...)
			got, err := pair.ours.AppendDecode(bytes.Clone(prefix), src)
			want, wantErr := pair.std.AppendDecode(bytes.Clone(prefix), src)
			if !bytes.Equal(got, want) || fmt.Sprint(err) != fmt.Sprint(wantErr) {
				t.Fatal("AppendDecode mismatch")
			}
		}
		var encoded bytes.Buffer
		w := NewEncoder(pair.ours, &encoded)
		for i := 0; i < len(raw); i += 7 {
			if _, err := w.Write(raw[i:min(i+7, len(raw))]); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if encoded.String() != pair.std.EncodeToString(raw) {
			t.Fatal("stream encode mismatch")
		}
		decoded, err := io.ReadAll(NewDecoder(pair.ours, &encoded))
		if err != nil || !bytes.Equal(decoded, raw) {
			t.Fatal("stream decode mismatch", err)
		}
	}
}

func TestVectorKernels(t *testing.T) {
	if !vectorAvailable {
		// TinyGo's WASI test runner cannot implement SkipNow/Goexit. The
		// compatibility tests still exercise the selected scalar backend.
		t.Log("SIMD backend unavailable; scalar compatibility tests apply")
		return
	}
	for _, pair := range encodings()[:4] {
		raw := randomBytes(257)
		encoded := []byte(pair.std.EncodeToString(raw))
		output := make([]byte, len(encoded))
		consumed := encodeVector(output, raw, pair.ours.kind == 2)
		if consumed < 12 || consumed%12 != 0 || !bytes.Equal(output[:consumed/3*4], encoded[:consumed/3*4]) {
			t.Fatal("vector encoder did not run correctly")
		}
		consumed, produced := decodeVector(output, encoded, pair.ours.kind == 2)
		if consumed < 16 || consumed%4 != 0 || produced != consumed/4*3 || !bytes.Equal(output[:produced], raw[:produced]) {
			t.Fatal("vector decoder did not run correctly")
		}
	}
}

func TestTightBuffersAndAlignment(t *testing.T) {
	for _, pair := range encodings()[:4] {
		for offset := 0; offset < 16; offset++ {
			for _, size := range []int{63, 64, 65, 255, 256, 257} {
				raw := randomBytes(size)
				want := pair.std.EncodeToString(raw)
				src := append(make([]byte, offset), raw...)[offset:]
				encoded := make([]byte, offset+len(want))[offset:]
				pair.ours.Encode(encoded, src)
				if string(encoded) != want {
					t.Fatal("unaligned encoding mismatch")
				}
				decoded := make([]byte, offset+size)[offset:]
				n, err := pair.ours.Decode(decoded, encoded)
				if err != nil || n != size || !bytes.Equal(decoded, raw) {
					t.Fatal("unaligned/tight decoding mismatch", pair.name, offset, size, err)
				}
			}
		}
	}
}

func TestZeroValueAndConfiguration(t *testing.T) {
	var ours Encoding
	var standard std.Encoding
	if ours.EncodeToString(randomBytes(128)) != standard.EncodeToString(randomBytes(128)) {
		t.Fatal("zero-value encoding mismatch")
	}
	checkDecode(t, encodingPair{"zero", &ours, &standard}, []byte(strings.Repeat("A", 128)))
	for _, pair := range encodings() {
		checkDecode(t, pair, []byte(strings.Repeat("\r\n", 100)))
		for n := 0; n < 512; n++ {
			if pair.ours.EncodedLen(n) != pair.std.EncodedLen(n) || pair.ours.DecodedLen(n) != pair.std.DecodedLen(n) {
				t.Fatal("length mismatch")
			}
		}
	}
}

func BenchmarkBase64(b *testing.B) {
	for _, size := range []int{64, 1024, 65536, 1048576} {
		raw := randomBytes(size)
		plain := []byte(std.StdEncoding.EncodeToString(raw))
		var mime []byte
		for start := 0; start < len(plain); start += 76 {
			mime = append(mime, plain[start:min(start+76, len(plain))]...)
			mime = append(mime, '\n')
		}
		for _, backend := range []struct {
			name   string
			encode func([]byte, []byte)
			decode func([]byte, []byte) (int, error)
		}{
			{"std", std.StdEncoding.Encode, std.StdEncoding.Decode},
			{"optimized", StdEncoding.Encode, StdEncoding.Decode},
		} {
			b.Run(fmt.Sprintf("%d/%s/encode", size, backend.name), func(b *testing.B) {
				dst := make([]byte, len(plain))
				b.SetBytes(int64(size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					backend.encode(dst, raw)
				}
			})
			for _, input := range []struct {
				name string
				src  []byte
			}{{"plain", plain}, {"mime", mime}} {
				b.Run(fmt.Sprintf("%d/%s/decode-%s", size, backend.name, input.name), func(b *testing.B) {
					dst := make([]byte, std.StdEncoding.DecodedLen(len(input.src)))
					b.SetBytes(int64(size))
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						n, err := backend.decode(dst, input.src)
						if err != nil || n != size {
							b.Fatal(n, err)
						}
					}
				})
			}
		}
	}
}

func BenchmarkBase64LineBreaks(b *testing.B) {
	raw := randomBytes(65536)
	plain := StdEncoding.EncodeToString(raw)
	for _, layout := range []struct {
		name, separator string
		width           int
	}{{"width32", "\n", 32}, {"width64", "\n", 64}, {"mimeCRLF", "\r\n", 76}} {
		var src []byte
		for start := 0; start < len(plain); start += layout.width {
			src = append(src, plain[start:min(start+layout.width, len(plain))]...)
			src = append(src, layout.separator...)
		}
		b.Run(layout.name, func(b *testing.B) {
			dst := make([]byte, StdEncoding.DecodedLen(len(src)))
			b.SetBytes(int64(len(raw)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				n, err := StdEncoding.Decode(dst, src)
				if err != nil || n != len(raw) {
					b.Fatal(n, err)
				}
			}
		})
	}
}
