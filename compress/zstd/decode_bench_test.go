package zstd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"testing"
)

// BenchmarkDecode decodes the reference CLI's frames from decode_fixtures_test.go,
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
			dst, err := DecodeAll(nil, c.frame)
			if err != nil {
				b.Fatal(err)
			}
			if len(dst) != goldenSize || fmt.Sprintf("%x", sha256.Sum256(dst)) != goldenSHA256 {
				b.Fatal("reference frame decoded incorrectly")
			}
			b.SetBytes(goldenSize)
			b.ReportAllocs()
			b.ResetTimer()
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
			defer r.Close()
			src.Reset(c.frame)
			if err := r.Reset(&src); err != nil {
				b.Fatal(err)
			}
			if n, err := io.Copy(io.Discard, r); err != nil || n != goldenSize {
				b.Fatalf("reference frame decoded %d bytes: %v", n, err)
			}
			b.SetBytes(goldenSize)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				src.Reset(c.frame)
				if err := r.Reset(&src); err != nil {
					b.Fatal(err)
				}
				if _, err := io.Copy(io.Discard, r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
