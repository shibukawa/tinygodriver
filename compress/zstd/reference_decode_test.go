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
		t.Fatalf("decoded length = %d, want %d", len(got), len(want))
	}
}
