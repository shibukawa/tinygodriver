package cbor

import (
	"errors"
	"testing"
)

// A byte offset locates a failure; a route names it. For a generated decoder
// the second is what turns "byte 47 is malformed" into "the field that is
// malformed is this one".
func TestErrorsCarryAContainerRoute(t *testing.T) {
	for _, tc := range []struct {
		name       string
		data       []byte
		validate   func([]byte) error
		wantOffset int64
		wantPath   string
	}{
		{
			// [1, 2, [3, <reserved additional information>]]
			name:       "nested arrays",
			data:       []byte{0x83, 0x01, 0x02, 0x82, 0x03, 0x1c},
			wantOffset: 5,
			wantPath:   "at [2][1]",
		},
		{
			// {1: [2, 1.5]} -- the float is what the world profile refuses
			name:       "a map value",
			data:       []byte{0xa1, 0x01, 0x82, 0x02, 0xf9, 0x3e, 0x00},
			validate:   func(b []byte) error { return World().Validate(b) },
			wantOffset: 4,
			wantPath:   "at {1}[1]",
		},
		{
			// {"players": [<reserved>]}
			name:       "a text key names the field",
			data:       []byte{0xa1, 0x67, 'p', 'l', 'a', 'y', 'e', 'r', 's', 0x81, 0x1c},
			validate:   func(b []byte) error { return World().Validate(b) },
			wantOffset: 10,
			wantPath:   `at {"players"}[0]`,
		},
		{
			// A failure in the root item has no route to report.
			name:       "the root item",
			data:       []byte{0x1c},
			wantOffset: 0,
			wantPath:   "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validate := tc.validate
			if validate == nil {
				validate = func(b []byte) error {
					r, err := NewReader(b, DecoderOptions{})
					if err != nil {
						return err
					}
					return r.Skip()
				}
			}
			err := validate(tc.data)
			var positioned *Error
			if !errors.As(err, &positioned) {
				t.Fatalf("err = %v, want a *cbor.Error", err)
			}
			if positioned.Offset != tc.wantOffset {
				t.Errorf("offset = %d, want %d", positioned.Offset, tc.wantOffset)
			}
			if positioned.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", positioned.Path, tc.wantPath)
			}
		})
	}
}

func TestErrorMessageIncludesBothWhenKnown(t *testing.T) {
	r, err := NewReader([]byte{0x83, 0x01, 0x02, 0x82, 0x03, 0x1c}, DecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := r.Skip().Error()
	want := "cbor: malformed input: reserved additional information 28 (at byte 5, at [2][1])"
	if got != want {
		t.Errorf("Error() = %q,\n         want %q", got, want)
	}
}

// The route is built by walking the input again, which is only affordable
// because it happens after something has already failed. Nothing may track it
// while decoding succeeds.
func TestTheRouteCostsNothingWhenNothingFails(t *testing.T) {
	data := worldSnapshot()
	r := World().ReaderOver(data)
	allocs := testingAllocs(func() {
		r.Reset(data)
		pairs, _, err := r.ReadMapHeader()
		if err != nil {
			t.Fatal(err)
		}
		for range pairs {
			if _, err := r.ReadUint(); err != nil {
				t.Fatal(err)
			}
			if err := r.Skip(); err != nil {
				t.Fatal(err)
			}
		}
	})
	if allocs != 0 {
		t.Errorf("a successful decode allocated %v times, want 0", allocs)
	}
}
