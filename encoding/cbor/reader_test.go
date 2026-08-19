package cbor

import (
	"bytes"
	"errors"
	"testing"
)

func mustReader(t *testing.T, data []byte, opts DecoderOptions) *Reader {
	t.Helper()
	r, err := NewReader(data, opts)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return r
}

// Skip is what lets a decoder tolerate a field it does not know. Every shape it
// can meet has to advance by exactly one item and no more.
func TestSkipAdvancesByExactlyOneItem(t *testing.T) {
	for _, tc := range []struct {
		name string
		item []byte
	}{
		{"small uint", []byte{0x05}},
		{"uint with argument", []byte{0x19, 0x01, 0x00}},
		{"negative", []byte{0x20}},
		{"byte string", []byte{0x43, 1, 2, 3}},
		{"text string", []byte{0x62, 'h', 'i'}},
		{"empty array", []byte{0x80}},
		{"nested array", []byte{0x82, 0x01, 0x82, 0x02, 0x03}},
		{"map", []byte{0xa2, 0x01, 0x02, 0x03, 0x04}},
		{"tagged", []byte{0xc1, 0x1a, 0x51, 0x4b, 0x67, 0xb0}},
		{"true", []byte{0xf5}},
		{"null", []byte{0xf6}},
		{"half float", []byte{0xf9, 0x3e, 0x00}},
		{"indefinite array", []byte{0x9f, 0x01, 0x02, 0xff}},
		{"indefinite map", []byte{0xbf, 0x01, 0x02, 0xff}},
		{"indefinite string", []byte{0x7f, 0x61, 'a', 0x61, 'b', 0xff}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A sentinel after the item catches a Skip that went too far, and
			// the offset check catches one that stopped short.
			data := append(append([]byte{}, tc.item...), 0x18, 0x2a)
			r := mustReader(t, data, DecoderOptions{})
			if err := r.Skip(); err != nil {
				t.Fatalf("Skip: %v", err)
			}
			if r.Offset() != len(tc.item) {
				t.Fatalf("Skip left offset %d, want %d", r.Offset(), len(tc.item))
			}
			v, err := r.ReadUint()
			if err != nil {
				t.Fatalf("reading the sentinel after Skip: %v", err)
			}
			if v != 42 {
				t.Fatalf("sentinel = %d, want 42", v)
			}
		})
	}
}

func TestSkipAllocatesNothing(t *testing.T) {
	data := []byte{0x83, 0x82, 0x01, 0x02, 0x63, 'a', 'b', 'c', 0xa1, 0x01, 0x02}
	r := mustReader(t, data, DecoderOptions{})
	allocs := testingAllocs(func() {
		r.Reset(data)
		if err := r.Skip(); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("Skip allocated %v times, want 0", allocs)
	}
}

// ReadRaw at depth is what a type carrying its own decoding needs: a field is
// never at the root. The reader decoder refuses this outright.
func TestReadRawCapturesASubItem(t *testing.T) {
	// [1, [2, 3], "tail"]
	data := []byte{0x83, 0x01, 0x82, 0x02, 0x03, 0x64, 't', 'a', 'i', 'l'}
	r := mustReader(t, data, DecoderOptions{})

	n, indefinite, err := r.ReadArrayHeader()
	if err != nil || indefinite || n != 3 {
		t.Fatalf("ReadArrayHeader = %d, %v, %v", n, indefinite, err)
	}
	if v, err := r.ReadUint(); err != nil || v != 1 {
		t.Fatalf("first item = %d, %v", v, err)
	}
	raw, err := r.ReadRaw()
	if err != nil {
		t.Fatalf("ReadRaw at depth: %v", err)
	}
	if want := []byte{0x82, 0x02, 0x03}; !bytes.Equal(raw, want) {
		t.Fatalf("ReadRaw = %x, want %x", raw, want)
	}
	if s, err := r.ReadText(); err != nil || s != "tail" {
		t.Fatalf("third item = %q, %v", s, err)
	}
	if !r.Done() {
		t.Fatalf("%d bytes left unconsumed", r.Remaining())
	}
}

func TestReadRawBorrowsFromTheInput(t *testing.T) {
	data := []byte{0x82, 0x01, 0x02}
	r := mustReader(t, data, DecoderOptions{})
	raw, err := r.ReadRaw()
	if err != nil {
		t.Fatal(err)
	}
	if &raw[0] != &data[0] {
		t.Error("ReadRaw copied, want a slice borrowed from the input")
	}
}

func TestPeekDoesNotConsume(t *testing.T) {
	data := []byte{0x63, 'a', 'b', 'c'}
	r := mustReader(t, data, DecoderOptions{})
	for range 3 {
		kind, err := r.Peek()
		if err != nil {
			t.Fatal(err)
		}
		if kind != TextString {
			t.Fatalf("Peek = %v, want text string", kind)
		}
		if r.Offset() != 0 {
			t.Fatalf("Peek consumed %d bytes", r.Offset())
		}
	}
	if s, err := r.ReadText(); err != nil || s != "abc" {
		t.Fatalf("ReadText = %q, %v", s, err)
	}
}

// The encoder writes the shortest form, so the declared width lives in the
// schema. A value outside it has to be a protocol error, not a silent wrap.
func TestSizedReadsEnforceTheDeclaredWidth(t *testing.T) {
	t.Run("in range", func(t *testing.T) {
		r := mustReader(t, AppendInt(nil, -32768), DecoderOptions{})
		v, err := r.ReadInt16()
		if err != nil || v != -32768 {
			t.Fatalf("ReadInt16 = %d, %v", v, err)
		}
	})
	t.Run("one past the floor", func(t *testing.T) {
		r := mustReader(t, AppendInt(nil, -32769), DecoderOptions{})
		if _, err := r.ReadInt16(); !errors.Is(err, ErrIntegerOverflow) {
			t.Fatalf("ReadInt16 = %v, want ErrIntegerOverflow", err)
		}
	})
	t.Run("one past the ceiling", func(t *testing.T) {
		r := mustReader(t, AppendUint(nil, 65536), DecoderOptions{})
		if _, err := r.ReadUint16(); !errors.Is(err, ErrIntegerOverflow) {
			t.Fatalf("ReadUint16 = %v, want ErrIntegerOverflow", err)
		}
	})
	t.Run("a negative does not satisfy an unsigned read", func(t *testing.T) {
		r := mustReader(t, AppendInt(nil, -1), DecoderOptions{})
		if _, err := r.ReadUint8(); !errors.Is(err, ErrUnexpectedToken) {
			t.Fatalf("ReadUint8 = %v, want ErrUnexpectedToken", err)
		}
	})
}

// A refusal has to say where. Rejecting one attestation needs the sentinel;
// comparing two builds that disagree needs the offset.
func TestErrorsCarryAnOffset(t *testing.T) {
	// [1, 2, <reserved additional information>]
	data := []byte{0x83, 0x01, 0x02, 0x1c}
	r := mustReader(t, data, DecoderOptions{})
	if _, _, err := r.ReadArrayHeader(); err != nil {
		t.Fatal(err)
	}
	_, _ = r.ReadUint()
	_, _ = r.ReadUint()
	err := r.Skip()
	var positioned *Error
	if !errors.As(err, &positioned) {
		t.Fatalf("Skip = %v, want a *cbor.Error", err)
	}
	if positioned.Offset != 3 {
		t.Errorf("offset = %d, want 3", positioned.Offset)
	}
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("error = %v, want it to still be ErrMalformed", err)
	}
}

func TestReaderRejectsTruncatedInput(t *testing.T) {
	for _, data := range [][]byte{
		{0x19, 0x01},             // uint declaring two argument bytes, one present
		{0x43, 1, 2},             // byte string declaring three, two present
		{0x82, 0x01},             // array of two, one present
		{0x9f, 0x01},             // indefinite array with no break
		{0x7f, 0x61, 'a'},        // indefinite string with no break
		{0xa2, 0x01, 0x02, 0x03}, // map of two pairs, three items present
	} {
		r := mustReader(t, data, DecoderOptions{})
		if err := r.Skip(); !errors.Is(err, ErrTruncated) {
			t.Errorf("Skip(%x) = %v, want ErrTruncated", data, err)
		}
	}
}
