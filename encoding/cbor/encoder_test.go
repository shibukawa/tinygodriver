package cbor

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"testing"
)

func encoder(t *testing.T, opts EncoderOptions) (*Encoder, *bytes.Buffer) {
	t.Helper()
	var b bytes.Buffer
	e, err := NewEncoder(&b, opts)
	if err != nil {
		t.Fatal(err)
	}
	return e, &b
}

func TestEncoderPrimitiveDeterministic(t *testing.T) {
	tests := []struct {
		name  string
		write func(*Encoder) error
		want  []byte
	}{
		{"uint23", func(e *Encoder) error { return e.WriteUint(23) }, []byte{0x17}},
		{"uint24", func(e *Encoder) error { return e.WriteUint(24) }, []byte{0x18, 0x18}},
		{"negative", func(e *Encoder) error { return e.WriteInt(-7) }, []byte{0x26}},
		{"text", func(e *Encoder) error { return e.WriteText("a") }, []byte{0x61, 'a'}},
		{"half", func(e *Encoder) error { return e.WriteFloat(1.5) }, []byte{0xf9, 0x3e, 0x00}},
		{"nan", func(e *Encoder) error { return e.WriteFloat(math.NaN()) }, []byte{0xf9, 0x7e, 0x00}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, b := encoder(t, EncoderOptions{})
			if err := tc.write(e); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b.Bytes(), tc.want) {
				t.Fatalf("got %x want %x", b.Bytes(), tc.want)
			}
		})
	}
}

func TestEncoderMapSortsCoreDeterministic(t *testing.T) {
	a, _ := MarshalText("a")
	e, b := encoder(t, EncoderOptions{})
	err := e.WriteMap([]MapEntry{{Key: a, Value: MarshalUint(1)}, {Key: MarshalUint(10), Value: MarshalUint(2)}})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0xa2, 0x0a, 0x02, 0x61, 'a', 0x01}
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("got %x want %x", b.Bytes(), want)
	}
}

func TestEncoderCOSEKeyRoundTrip(t *testing.T) {
	e, b := encoder(t, EncoderOptions{})
	err := e.WriteMap([]MapEntry{
		{MarshalInt(1), MarshalInt(2)}, {MarshalInt(3), MarshalInt(-7)},
		{MarshalInt(-1), MarshalInt(1)}, {MarshalInt(-2), MarshalBytes([]byte{1, 2})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(b.Bytes(), DecoderOptions{RejectDuplicateMapKeys: true}); err != nil {
		t.Fatal(err)
	}
}

func TestEncoderRejectsNonDeterministicRaw(t *testing.T) {
	tests := []RawMessage{
		{0x18, 0x00},                         // non-minimal integer
		{0x9f, 0x01, 0xff},                   // indefinite array
		{0xa2, 0x61, 'a', 0x01, 0x00, 0x02},  // unsorted keys
		{0xfb, 0x3f, 0xf8, 0, 0, 0, 0, 0, 0}, // 1.5 encoded as float64
	}
	for _, raw := range tests {
		e, _ := encoder(t, EncoderOptions{})
		if err := e.WriteRaw(raw); err == nil {
			t.Fatalf("accepted %x", raw)
		}
	}
}

func TestEncoderRejectsDuplicateMapKey(t *testing.T) {
	e, _ := encoder(t, EncoderOptions{})
	err := e.WriteMap([]MapEntry{{MarshalUint(1), MarshalNull()}, {MarshalUint(1), MarshalBool(true)}})
	if !errors.Is(err, ErrDuplicateMapKey) {
		t.Fatalf("error = %v", err)
	}
}

func TestExactHalfSubnormal(t *testing.T) {
	v := math.Ldexp(1, -24)
	if got := MarshalFloat(v); !bytes.Equal(got, RawMessage{0xf9, 0, 1}) {
		t.Fatalf("got %x", got)
	}
}

func ExampleDecoder() {
	data := []byte{0xa2, 0x01, 0x02, 0x20, 0x42, 0xca, 0xfe}
	d, _ := NewDecoder(bytes.NewReader(data), DecoderOptions{RejectDuplicateMapKeys: true})
	pairs, _, _ := d.ReadMap()
	fmt.Println(pairs)
	// Output:
	// 2
}

func ExampleEncoder() {
	var out bytes.Buffer
	e, _ := NewEncoder(&out, EncoderOptions{})
	key, _ := MarshalText("alg")
	_ = e.WriteMap([]MapEntry{{Key: key, Value: MarshalInt(-7)}})
	fmt.Println(len(out.Bytes()))
	// Output:
	// 6
}
