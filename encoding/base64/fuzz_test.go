//go:build !tinygo

package base64

import (
	"bytes"
	"fmt"
	"testing"
)

func FuzzDecodeCompatibility(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte("Zg=="), []byte("AB=="), []byte("aGVsbG8=\r\n"), []byte(StdEncoding.EncodeToString(randomBytes(128)))} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 16384 {
			return
		}
		for _, pair := range encodings() {
			want, wantErr := pair.std.DecodeString(string(src))
			got, gotErr := pair.ours.DecodeString(string(src))
			if !bytes.Equal(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				t.Fatalf("%s: %x %v != %x %v", pair.name, got, gotErr, want, wantErr)
			}
		}
	})
}
