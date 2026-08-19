package cbor

import (
	"bytes"
	"errors"
	"testing"
)

func dupOptions() DecoderOptions {
	return DecoderOptions{
		MaxInputBytes:          4096,
		MaxRawMessageBytes:     4096,
		RejectDuplicateMapKeys: true,
	}
}

// Two keys are duplicates when they denote the same value, however each was
// spelled. Comparing encoded bytes would miss every case below.
func TestDuplicateKeysAreFoundAcrossEncodings(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{
			// 5 written minimally, then again with a one-byte argument.
			"non-minimal argument",
			[]byte{0xa2, 0x05, 0x01, 0x18, 0x05, 0x02},
		},
		{
			// "ab" definite, then "a"+"b" as indefinite chunks.
			"indefinite string chunking",
			[]byte{0xa2, 0x62, 'a', 'b', 0x01, 0x7f, 0x61, 'a', 0x61, 'b', 0xff, 0x02},
		},
		{
			// {1:2, 3:4} and {3:4, 1:2} are the same map.
			"map member order",
			[]byte{0xa2, 0xa2, 0x01, 0x02, 0x03, 0x04, 0x01, 0xa2, 0x03, 0x04, 0x01, 0x02, 0x02},
		},
		{
			// 1.5 as a half, then as a double.
			"float width",
			[]byte{0xa2, 0xf9, 0x3e, 0x00, 0x01, 0xfb, 0x3f, 0xf8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.data, dupOptions()); !errors.Is(err, ErrDuplicateMapKey) {
				t.Errorf("Validate = %v, want ErrDuplicateMapKey", err)
			}
		})
	}
}

// The converse: values that merely look similar are distinct keys, and
// canonicalizing must not collapse them.
func TestDistinctKeysAreNotCollapsed(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{
			// The integer 1 and the text "1".
			"integer against text",
			[]byte{0xa2, 0x01, 0x01, 0x61, '1', 0x02},
		},
		{
			// Arrays are ordered, so [1,2] and [2,1] differ.
			"array order",
			[]byte{0xa2, 0x82, 0x01, 0x02, 0x01, 0x82, 0x02, 0x01, 0x02},
		},
		{
			// 1 and -1 share an argument and differ in major type.
			"unsigned against negative",
			[]byte{0xa2, 0x01, 0x01, 0x20, 0x02},
		},
		{
			// The byte string 0x61 and the text string "a" have the same payload.
			"bytes against text",
			[]byte{0xa2, 0x41, 0x61, 0x01, 0x61, 'a', 0x02},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.data, dupOptions()); err != nil {
				t.Errorf("Validate = %v, want the two keys accepted as distinct", err)
			}
		})
	}
}

// The detector runs over unauthenticated input, so its cost has to follow the
// input rather than the shape of it. This is the case the recursive string
// concatenation it replaced was quadratic in.
func BenchmarkDuplicateDetectionNestedKeys(b *testing.B) {
	var buf bytes.Buffer
	buf.WriteByte(0xa8) // map, 8 pairs
	for i := range 8 {
		buf.Write([]byte{0x84}) // key: array of 4
		for j := range 4 {
			buf.Write([]byte{0x82, byte(i), byte(j)}) // [i, j]
		}
		buf.WriteByte(0x01) // value
	}
	data := buf.Bytes()
	opts := dupOptions()

	b.ReportAllocs()
	for b.Loop() {
		if err := Validate(data, opts); err != nil {
			b.Fatal(err)
		}
	}
}
