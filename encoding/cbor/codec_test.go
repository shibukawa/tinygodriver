package cbor

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// fixed64 stands in for a fixed-point value from a math library this package
// must never import: a defined type over int64 whose scale lives in the schema.
// It reaches the wire as a bare integer, and it carries its own encoding, which
// is the whole point -- neither package knows the other exists, and the method
// set is the only seam between them.
type fixed64 int64

func (f fixed64) AppendCBORTo(dst []byte) []byte { return AppendInt(dst, int64(f)) }

func (f *fixed64) DecodeCBORFrom(data []byte) error {
	r := ReaderOver(data, DecoderOptions{})
	v, err := r.ReadInt()
	if err != nil {
		return err
	}
	if !r.Done() {
		return ErrExtraneousData
	}
	*f = fixed64(v)
	return nil
}

var (
	_ Appender  = fixed64(0)
	_ Decodable = (*fixed64)(nil)
)

// playerInput is the shape a generator would emit for the wire profile: a fixed
// field order, no field names, one sized integer per field, and a foreign type
// among them.
type playerInput struct {
	Tick    uint32
	MoveX   fixed64
	MoveY   fixed64
	Buttons uint16
}

func (p playerInput) AppendCBORTo(dst []byte) []byte {
	dst = AppendArrayHeader(dst, 4)
	dst = AppendUint(dst, uint64(p.Tick))
	dst = p.MoveX.AppendCBORTo(dst)
	dst = p.MoveY.AppendCBORTo(dst)
	return AppendUint(dst, uint64(p.Buttons))
}

func (p *playerInput) DecodeCBORFrom(data []byte) error {
	r := ReaderOver(data, DecoderOptions{})
	return p.decodeFrom(&r)
}

func (p *playerInput) decodeFrom(r *Reader) error {
	n, indefinite, err := r.ReadArrayHeader()
	if err != nil {
		return err
	}
	if indefinite || n != 4 {
		return fmt.Errorf("%w: want an array of 4", ErrUnexpectedToken)
	}
	if p.Tick, err = r.ReadUint32(); err != nil {
		return err
	}
	// A generator resolves the foreign type at generation time and emits one
	// named call, so there is no runtime type switch on this path.
	raw, err := r.ReadRaw()
	if err != nil {
		return err
	}
	if err := p.MoveX.DecodeCBORFrom(raw); err != nil {
		return err
	}
	if raw, err = r.ReadRaw(); err != nil {
		return err
	}
	if err := p.MoveY.DecodeCBORFrom(raw); err != nil {
		return err
	}
	p.Buttons, err = r.ReadUint16()
	return err
}

// The wire form is part of the protocol, so it is pinned here rather than
// described. These bytes must not change without the protocol version changing
// with them, and they must be the same bytes on every target.
func TestWireMessageEncodesToPinnedBytes(t *testing.T) {
	input := playerInput{Tick: 1234, MoveX: -1, MoveY: 0, Buttons: 3}
	want := []byte{
		0x84,             // array of 4
		0x19, 0x04, 0xd2, // 1234
		0x20, // -1
		0x00, // 0
		0x03, // 3
	}
	got := input.AppendCBORTo(nil)
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded %x, want %x", got, want)
	}
	if err := Wire().Validate(got); err != nil {
		t.Fatalf("the pinned bytes are not legal under the wire profile: %v", err)
	}
}

func TestWireMessageRoundTrips(t *testing.T) {
	for _, want := range []playerInput{
		{Tick: 0, MoveX: 0, MoveY: 0, Buttons: 0},
		{Tick: 1234, MoveX: -1, MoveY: 0, Buttons: 3},
		{Tick: 4294967295, MoveX: -2147483648, MoveY: 2147483647, Buttons: 65535},
	} {
		encoded := want.AppendCBORTo(nil)
		if err := Wire().Validate(encoded); err != nil {
			t.Fatalf("%+v encoded to %x, which the profile refuses: %v", want, encoded, err)
		}
		var got playerInput
		if err := got.DecodeCBORFrom(encoded); err != nil {
			t.Fatalf("decoding %x: %v", encoded, err)
		}
		if got != want {
			t.Errorf("round trip gave %+v, want %+v", got, want)
		}
		// Encoding what was decoded must reproduce the same bytes, which is the
		// property a replay compares digests on.
		if again := got.AppendCBORTo(nil); !bytes.Equal(again, encoded) {
			t.Errorf("re-encoded to %x, want %x", again, encoded)
		}
	}
}

// The steady state is what matters: one buffer and one reader per connection,
// reused for every message. A fixed-shape wire message must cost nothing per
// tick beyond the bytes themselves.
func TestFixedShapeMessageIsZeroAllocationInSteadyState(t *testing.T) {
	input := playerInput{Tick: 1234, MoveX: -1, MoveY: 1, Buttons: 3}
	buf := make([]byte, 0, 64)

	encodeAllocs := testingAllocs(func() {
		buf = input.AppendCBORTo(buf[:0])
	})
	if encodeAllocs != 0 {
		t.Errorf("encoding allocated %v times, want 0", encodeAllocs)
	}

	encoded := input.AppendCBORTo(nil)
	r := ReaderOver(encoded, DecoderOptions{})
	var out playerInput
	decodeAllocs := testingAllocs(func() {
		r.Reset(encoded)
		if err := out.decodeFrom(&r); err != nil {
			t.Fatal(err)
		}
	})
	if decodeAllocs != 0 {
		t.Errorf("decoding allocated %v times, want 0", decodeAllocs)
	}
	if out != input {
		t.Errorf("decoded %+v, want %+v", out, input)
	}
}

// A field whose declared width the message overruns is a protocol error, caught
// where it happens rather than wrapped into a plausible value.
func TestAWideValueInANarrowFieldIsRefused(t *testing.T) {
	// Tick is uint32; 2^32 is one past it.
	dst := AppendArrayHeader(nil, 4)
	dst = AppendUint(dst, 1<<32)
	dst = AppendInt(dst, 0)
	dst = AppendInt(dst, 0)
	dst = AppendUint(dst, 0)

	var got playerInput
	if err := got.DecodeCBORFrom(dst); !errors.Is(err, ErrIntegerOverflow) {
		t.Fatalf("decode = %v, want ErrIntegerOverflow", err)
	}
}

// AppendNegative reaches the half of the negative range AppendInt cannot, which
// the decoder has always been able to read.
func TestAppendNegativeReachesTheWholeRange(t *testing.T) {
	// -1 - (2^63) is one past the int64 floor.
	encoded := AppendNegative(nil, 1<<63)
	r := ReaderOver(encoded, DecoderOptions{})
	if _, err := r.ReadInt(); !errors.Is(err, ErrIntegerOverflow) {
		t.Fatalf("ReadInt = %v, want ErrIntegerOverflow for a value below the int64 floor", err)
	}
	// The item is still well formed, and the raw form still round-trips.
	if err := Validate(encoded, DecoderOptions{}); err != nil {
		t.Fatalf("Validate = %v, want the item accepted", err)
	}
	if want := []byte{0x3b, 0x80, 0, 0, 0, 0, 0, 0, 0}; !bytes.Equal(encoded, want) {
		t.Fatalf("encoded %x, want %x", encoded, want)
	}
}
