package cbor

import (
	"bytes"
	"errors"
	"testing"
)

// The keys -1 and 100 encode as 0x20 and 0x18 0x64. Length-first puts the
// one-byte key first; bytewise puts 0x18 first. Every other pair of keys in
// this package's tests happens to order the same way under both rules, which is
// how the documentation managed to claim the wrong one for so long.
var (
	keyNegOne  = MarshalInt(-1)   // 0x20
	keyHundred = MarshalUint(100) // 0x18 0x64
)

func encodeMapWith(t *testing.T, order KeyOrder, entries []MapEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	encoder, err := NewEncoder(&buf, EncoderOptions{KeyOrder: order})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := encoder.WriteMap(entries); err != nil {
		t.Fatalf("WriteMap under %s: %v", order, err)
	}
	return buf.Bytes()
}

func TestKeyOrderingsDisagree(t *testing.T) {
	entries := []MapEntry{
		{Key: keyHundred, Value: MarshalUint(2)},
		{Key: keyNegOne, Value: MarshalUint(1)},
	}

	lengthFirst := encodeMapWith(t, LengthFirstKeyOrder, entries)
	bytewise := encodeMapWith(t, BytewiseKeyOrder, entries)

	wantLengthFirst := []byte{0xa2, 0x20, 0x01, 0x18, 0x64, 0x02}
	wantBytewise := []byte{0xa2, 0x18, 0x64, 0x02, 0x20, 0x01}

	if !bytes.Equal(lengthFirst, wantLengthFirst) {
		t.Errorf("length-first = %x, want %x", lengthFirst, wantLengthFirst)
	}
	if !bytes.Equal(bytewise, wantBytewise) {
		t.Errorf("bytewise = %x, want %x", bytewise, wantBytewise)
	}
	if bytes.Equal(lengthFirst, bytewise) {
		t.Fatal("the two orderings produced the same bytes, so this vector proves nothing")
	}
}

func TestZeroValueKeyOrderIsCTAP2(t *testing.T) {
	entries := []MapEntry{
		{Key: keyHundred, Value: MarshalUint(2)},
		{Key: keyNegOne, Value: MarshalUint(1)},
	}
	zero := encodeMapWith(t, KeyOrder(0), entries)
	explicit := encodeMapWith(t, LengthFirstKeyOrder, entries)
	if !bytes.Equal(zero, explicit) {
		t.Fatalf("zero value = %x, want the length-first ordering %x", zero, explicit)
	}
}

func TestWriteRawEnforcesTheSelectedOrder(t *testing.T) {
	lengthFirst := []byte{0xa2, 0x20, 0x01, 0x18, 0x64, 0x02}
	bytewise := []byte{0xa2, 0x18, 0x64, 0x02, 0x20, 0x01}

	for _, tc := range []struct {
		name     string
		order    KeyOrder
		accepted []byte
		refused  []byte
	}{
		{"length-first", LengthFirstKeyOrder, lengthFirst, bytewise},
		{"bytewise", BytewiseKeyOrder, bytewise, lengthFirst},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoder, err := NewEncoder(&bytes.Buffer{}, EncoderOptions{KeyOrder: tc.order})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := encoder.WriteRaw(tc.accepted); err != nil {
				t.Errorf("WriteRaw(%x) under %s: %v", tc.accepted, tc.order, err)
			}
			err = encoder.WriteRaw(tc.refused)
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("WriteRaw(%x) under %s = %v, want ErrMalformed", tc.refused, tc.order, err)
			}
		})
	}
}
