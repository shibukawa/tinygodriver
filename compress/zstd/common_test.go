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
