//go:build force_tinygo_logic

package zstd

import (
	"bytes"
	"testing"
)

// TestRepeatOffsetCarriesAcrossBlocks pins the cross-block repeat-offset
// contract. A decoder resets its repeat-offset history only at frame start and
// carries it across the blocks inside a frame, so an encoder that models the
// history per block disagrees with it in exactly one spot: the first
// Offset_Value 1 of a later block, which the decoder resolves to the previous
// block's last offset. The frame then decodes to the wrong bytes at the right
// length, which no length check catches.
//
// The input is built to land on that spot. The first block ends with matches
// at offset 32, so the decoder enters the second block with 32 in slot one;
// the second block opens with a few literals and then a byte run, whose
// matches sit at offset 1 -- the one distance a per-block model would call
// slot one. Only the reference decoder can convict the output, so the real
// assertion is assertReferenceDecode.
func TestRepeatOffsetCarriesAcrossBlocks(t *testing.T) {
	unit := make([]byte, 32)
	for i := range unit {
		unit[i] = byte(i*37 + 11)
	}

	var src []byte
	for len(src) < maxBlockSize {
		src = append(src, unit...)
	}
	src = src[:maxBlockSize]

	src = append(src, "second block literals:"...)
	src = append(src, bytes.Repeat([]byte{'a'}, 64)...)
	for i := 0; i < 64; i++ {
		src = append(src, unit...)
	}

	encoded, _, err := EncodeAll(src, WithETag(false))
	if err != nil {
		t.Fatalf("EncodeAll: %v", err)
	}
	assertReferenceDecode(t, encoded, src)
}
