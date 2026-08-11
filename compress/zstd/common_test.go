package zstd

import (
	"bytes"
	"testing"
)

func TestETagOption(t *testing.T) {
	for _, test := range []struct {
		name    string
		options []Option
		enabled bool
	}{
		{name: "default", enabled: true},
		{name: "enabled", options: []Option{WithETag(true)}, enabled: true},
		{name: "disabled", options: []Option{WithETag(false)}, enabled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, result, err := EncodeAll([]byte("cacheable response"), test.options...)
			if err != nil {
				t.Fatal(err)
			}
			if result.Size != int64(len(encoded)) || result.ETagEnabled != test.enabled {
				t.Fatalf("result = %#v, encoded size = %d", result, len(encoded))
			}
			if test.enabled {
				if result.ETag() == "" || result.SHA256 == ([32]byte{}) {
					t.Fatalf("enabled ETag result = %#v", result)
				}
			} else if result.ETag() != "" || result.SHA256 != ([32]byte{}) {
				t.Fatalf("disabled ETag result = %#v", result)
			}
		})
	}
}

func TestWithETagFalseSkipsHasherAllocation(t *testing.T) {
	w := newOutputWriter(&bytes.Buffer{}, false)
	if w.hash != nil {
		t.Fatal("WithETag(false) allocated a hasher")
	}
}

// TestResetRepeatsRepresentation is what makes the encoder poolable: a Writer
// that has finished one frame and been Reset must produce byte for byte what a
// fresh Writer would, and account for it as if nothing came before.
func TestResetRepeatsRepresentation(t *testing.T) {
	const payload = "reset me, and then reset me again, and again"

	fresh, freshResult, err := EncodeAll([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}

	var dst bytes.Buffer
	z, err := NewWriter(&dst)
	if err != nil {
		t.Fatal(err)
	}
	// The first frame carries different input on purpose: reused state that
	// leaked into the second frame would show up as a different encoding.
	if _, err := z.Write([]byte("a first, longer frame to leave state behind")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}

	for round := range 3 {
		dst.Reset()
		z.Reset(&dst)
		if _, err := z.Write([]byte(payload)); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if err := z.Close(); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		result, err := z.Result()
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if !bytes.Equal(dst.Bytes(), fresh) {
			t.Fatalf("round %d: %d bytes, want the %d a fresh writer produced",
				round, dst.Len(), len(fresh))
		}
		if result != freshResult {
			t.Fatalf("round %d: result = %#v, want %#v", round, result, freshResult)
		}
	}
}

func TestResetDefersFrameHeader(t *testing.T) {
	var first, second bytes.Buffer
	z, err := NewWriter(&first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := z.Write([]byte("committed")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	z.Reset(&second)
	if second.Len() != 0 {
		t.Fatalf("Reset wrote %d bytes; nothing may reach the destination until "+
			"the caller does", second.Len())
	}
}

// TestResetKeepsBlockBuffer is what makes pooling worth doing: an encoder that
// gave its 128 KiB block buffer back at Close would allocate a new one per
// response, which is most of what a pool exists to avoid. Skipped on the
// klauspost backend, which holds its buffers inside its own encoder.
func TestResetKeepsBlockBuffer(t *testing.T) {
	var dst bytes.Buffer
	z, err := NewWriter(&dst)
	if err != nil {
		t.Fatal(err)
	}
	before := blockBufferCap(z)
	if before == 0 {
		t.Skip("this backend keeps no block buffer of its own")
	}
	if _, err := z.Write([]byte("something to encode")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	dst.Reset()
	z.Reset(&dst)
	if got := blockBufferCap(z); got != before {
		t.Fatalf("block buffer capacity %d after Close and Reset, want the original %d",
			got, before)
	}
}

func TestResetRejectsNil(t *testing.T) {
	z, err := NewWriter(&bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	z.Reset(nil)
	if _, err := z.Write([]byte("nowhere to go")); err == nil {
		t.Fatal("Write to a Writer reset onto nil reported no error")
	}
}

func TestETagOptionDoesNotChangeRepresentation(t *testing.T) {
	withETag, _, err := EncodeAll([]byte("same encoded response"), WithETag(true))
	if err != nil {
		t.Fatal(err)
	}
	withoutETag, _, err := EncodeAll([]byte("same encoded response"), WithETag(false))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(withETag, withoutETag) {
		t.Fatal("ETag option changed encoded representation")
	}
}
