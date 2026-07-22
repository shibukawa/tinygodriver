//go:build tinygo || force_tinygo_logic

package zstd

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestEncodeAllRawFrameAndResult(t *testing.T) {
	encoded, result, err := EncodeAll([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x38, 0x29, 0x00, 0x00, 'h', 'e', 'l', 'l', 'o'}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("encoded = %x, want %x", encoded, want)
	}
	wantHash := sha256.Sum256(encoded)
	if result.SHA256 != wantHash || result.Size != int64(len(encoded)) {
		t.Fatalf("result = %#v, want size %d hash %x", result, len(encoded), wantHash)
	}
	if got := result.ETag(); got != `"sha256-13fa105c5bc631c9f31da372347ebdd870268f4b26a56b28137430877e2d768b"` {
		t.Fatalf("ETag = %q", got)
	}
}

func TestEncodeAllUsesRLE(t *testing.T) {
	src := bytes.Repeat([]byte{'x'}, 1000)
	encoded, _, err := EncodeAll(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 10 {
		t.Fatalf("encoded length = %d, want 10", len(encoded))
	}
	assertReferenceDecode(t, encoded, src)
}

func TestEncodeAllSplitsInteriorRLE(t *testing.T) {
	src := []byte("prefix------------suffix")
	encoded, _, err := EncodeAll(src)
	if err != nil {
		t.Fatal(err)
	}
	// Frame header (6), two raw blocks (3+6 and 3+6), and one RLE block (4).
	if len(encoded) != 28 {
		t.Fatalf("encoded length = %d, want 28", len(encoded))
	}
	assertReferenceDecode(t, encoded, src)
}

func TestEncodeAllUsesCompressedBlock(t *testing.T) {
	src := bytes.Repeat([]byte("tinygodriver response "), 100)
	encoded, _, err := EncodeAll(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= len(src)/4 {
		t.Fatalf("encoded length = %d, want less than %d", len(encoded), len(src)/4)
	}
	if blockType := (encoded[6] >> 1) & 3; blockType != 2 {
		t.Fatalf("block type = %d, want compressed block", blockType)
	}
	assertReferenceDecode(t, encoded, src)
}

func TestWriterStreamsBoundedBlocks(t *testing.T) {
	src := make([]byte, maxBlockSize+73)
	for i := range src {
		src[i] = byte(i)
	}
	var dst bytes.Buffer
	z, err := NewWriter(&dst)
	if err != nil {
		t.Fatal(err)
	}
	for start := 0; start < len(src); start += 997 {
		end := start + 997
		if end > len(src) {
			end = len(src)
		}
		if _, err := z.Write(src[start:end]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := z.Result(); !errors.Is(err, ErrResultUnavailable) {
		t.Fatalf("Result before Close error = %v", err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := z.Write(nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Write after Close error = %v", err)
	}
	result, err := z.Result()
	if err != nil {
		t.Fatal(err)
	}
	if result.Size != int64(dst.Len()) || result.SHA256 != sha256.Sum256(dst.Bytes()) {
		t.Fatalf("result does not describe output")
	}
	assertReferenceDecode(t, dst.Bytes(), src)
}

func TestEncodeAllEmpty(t *testing.T) {
	encoded, _, err := EncodeAll(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x38, 0x01, 0x00, 0x00}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("encoded = %x, want %x", encoded, want)
	}
	assertReferenceDecode(t, encoded, nil)
}

func TestNewWriterRejectsNil(t *testing.T) {
	if _, err := NewWriter(nil); err == nil {
		t.Fatal("NewWriter(nil) succeeded")
	}
}
