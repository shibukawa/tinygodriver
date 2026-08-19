//go:build !tinygo

package cbor

import "testing"

func FuzzValidate(f *testing.F) {
	for _, seed := range [][]byte{{0x00}, {0x80}, {0xa0}, {0xff}, {0x9f, 0x01, 0xff}, {0xa1, 0x01, 0x02}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = Validate(data, DecoderOptions{MaxInputBytes: 4096, MaxRawMessageBytes: 4096, RejectDuplicateMapKeys: true})
	})
}
