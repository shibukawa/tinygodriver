//go:build !tinygo

package zstd

import (
	"bytes"
	"os/exec"
	"testing"
)

func assertReferenceDecode(t *testing.T, encoded, want []byte) {
	t.Helper()
	path, err := exec.LookPath("zstd")
	if err != nil {
		t.Skip("zstd CLI is not installed")
	}
	cmd := exec.Command(path, "-q", "-d", "-c")
	cmd.Stdin = bytes.NewReader(encoded)
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("zstd decode: %v", err)
	}
	if !bytes.Equal(got, want) {
		if len(got) != len(want) {
			t.Fatalf("decoded length = %d, want %d", len(got), len(want))
		}
		// Same length, different bytes is the silent-corruption shape, and the
		// first divergence is the datum that identifies it.
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("decoded bytes diverge at offset %d: got %#x, want %#x", i, got[i], want[i])
			}
		}
	}
}
