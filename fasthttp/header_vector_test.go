package fasthttp

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/shibukawa/tinygodriver/internal/bytealg"
)

func TestHeaderValueVectorCompatibility(t *testing.T) {
	for c := range 256 {
		for lane := range 80 {
			value := bytes.Repeat([]byte{'a'}, 80)
			value[lane] = byte(c)
			if bytealg.ValidHeaderValue(value) != validHeaderValueByte(byte(c)) {
				t.Fatalf("header byte=%d lane=%d differs from the protocol table", c, lane)
				return
			}
		}
	}
}

func BenchmarkHeaderValueValidation(b *testing.B) {
	for _, size := range []int{16, 64, 1024, 4096} {
		for _, selected := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/selected=%t", size, selected), func(b *testing.B) {
				value := bytes.Repeat([]byte{'a'}, size)
				b.SetBytes(int64(size))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if selected && bytealg.Available && len(value) >= 64 {
						if !bytealg.ValidHeaderValue(value) {
							b.Fatal("invalid value")
							return
						}
					} else {
						for _, c := range value {
							if !validHeaderValueByte(c) {
								b.Fatal("invalid value")
								return
							}
						}
					}
				}
			})
		}
	}
}
