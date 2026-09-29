//go:build tinygo || force_tinygo_logic

// Benchmarks for the bounded encoder, over the payload shapes the ratio tests
// use: markup and JSON because that is what a web server sends, random bytes
// because the stored fallback has to stay cheap when compression cannot win,
// and a small page because most responses never fill a block. They run under
// force_tinygo_logic for the same reason ratio_test.go does — that is what
// selects this encoder on host Go, where the profiler and benchstat live.

package zstd

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"testing"
)

// benchPayload grows a deterministic document to at least size bytes by
// repeating a structured generator, which is what keeps the match finder busy
// the way real markup does.
func benchHTML(size int) []byte {
	var b bytes.Buffer
	b.WriteString("<!doctype html><html><head><title>Index</title></head><body>\n")
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "  <li class=%q data-id=%q><a href=\"/items/%d\">Item %d</a></li>\n",
			"item", fmt.Sprint(i), i, i)
	}
	b.WriteString("</body></html>\n")
	return b.Bytes()
}

func benchJSON(size int) []byte {
	var b bytes.Buffer
	b.WriteString(`{"items":[`)
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"item-%d","active":true,"score":%d.5}`, i, i, i%97)
	}
	b.WriteString(`]}`)
	return b.Bytes()
}

func benchRandom(size int) []byte {
	p := make([]byte, size)
	rand.New(rand.NewSource(1)).Read(p)
	return p
}

func benchPayloads() []struct {
	name string
	data []byte
} {
	return []struct {
		name string
		data []byte
	}{
		{"html64k", benchHTML(64 << 10)},
		{"json64k", benchJSON(64 << 10)},
		{"random64k", benchRandom(64 << 10)},
		{"small2k", benchHTML(2 << 10)},
	}
}

func BenchmarkEncodeAll(b *testing.B) {
	for _, p := range benchPayloads() {
		b.Run(p.name, func(b *testing.B) {
			b.SetBytes(int64(len(p.data)))
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := EncodeAll(p.data, WithETag(false)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkWriterReuse is the pooled path: one Writer reset per frame, which is
// what separates per-block garbage from the encoder's fixed footprint.
func BenchmarkWriterReuse(b *testing.B) {
	for _, p := range benchPayloads() {
		b.Run(p.name, func(b *testing.B) {
			var dst bytes.Buffer
			dst.Grow(len(p.data) + 1024)
			z, err := NewWriter(&dst, WithETag(false))
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(p.data)))
			b.ReportAllocs()
			for b.Loop() {
				dst.Reset()
				z.Reset(&dst)
				if _, err := z.Write(p.data); err != nil {
					b.Fatal(err)
				}
				if err := z.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDecode decodes the reference CLI's frames from decoder_test.go,
// which exercise the whole format rather than the subset this encoder writes,
// through DecodeAll and through a Reset Reader.
func BenchmarkDecode(b *testing.B) {
	for _, c := range []struct {
		name  string
		frame []byte
	}{
		{"level6-1k-blocks", goldenSmallBlocks},
		{"level19", goldenLevel19},
		{"level1-stdin", goldenStdin},
	} {
		b.Run("DecodeAll/"+c.name, func(b *testing.B) {
			var dst []byte
			b.SetBytes(goldenSize)
			b.ReportAllocs()
			for range b.N { // TinyGo does not implement b.Loop
				var err error
				if dst, err = DecodeAll(dst[:0], c.frame); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("Reader/"+c.name, func(b *testing.B) {
			var src bytes.Reader
			r, err := NewReader(&src)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(goldenSize)
			b.ReportAllocs()
			for range b.N {
				src.Reset(c.frame)
				r.Reset(&src)
				if _, err := io.Copy(io.Discard, r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
