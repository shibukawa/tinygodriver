//go:build !tinygo

package hex

import (
	"bytes"
	std "encoding/hex"
	"testing"
)

func FuzzCompatibility(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte("0"), []byte("xx"), []byte("00aBff"), bytes.Repeat([]byte("01aB9fCd"), 32)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 65536 {
			return
		}
		checkDecode(t, src)
		if EncodeToString(src) != std.EncodeToString(src) {
			t.Fatal("encoding differs")
		}
	})
}
