//go:build !tinygo && !force_tinygo_logic

package zstd

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"

	kzstd "github.com/klauspost/compress/zstd"
)

func TestKlauspostEncodeAllAndResult(t *testing.T) {
	src := bytes.Repeat([]byte("tinygodriver host response "), 100)
	encoded, result, err := EncodeAll(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= len(src)/4 {
		t.Fatalf("encoded length = %d, want less than %d", len(encoded), len(src)/4)
	}
	if result.Size != int64(len(encoded)) || result.SHA256 != sha256.Sum256(encoded) {
		t.Fatalf("result does not identify encoded output")
	}
	decoder, err := kzstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	decoded, err := decoder.DecodeAll(encoded, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, src) {
		t.Fatalf("decoded length = %d, want %d", len(decoded), len(src))
	}
}

func TestKlauspostWriterLifecycle(t *testing.T) {
	var dst bytes.Buffer
	z, err := NewWriter(&dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := z.Result(); !errors.Is(err, ErrResultUnavailable) {
		t.Fatalf("Result before Close error = %v", err)
	}
	if _, err := z.Write([]byte("response")); err != nil {
		t.Fatal(err)
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
		t.Fatalf("result does not identify streamed output")
	}
}
