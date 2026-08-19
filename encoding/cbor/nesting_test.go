package cbor

import (
	"bytes"
	"errors"
	"testing"
)

// nestedArrays builds n nested single-element arrays around a 0.
func nestedArrays(n int) []byte {
	b := make([]byte, 0, n+1)
	for range n {
		b = append(b, 0x81)
	}
	return append(b, 0x00)
}

// Everywhere this package drives the recursion, the depth is bounded.
func TestNestingIsBoundedWhereThePackageWalks(t *testing.T) {
	opts := DecoderOptions{MaxNestedLevels: 4, MaxInputBytes: 1 << 20}
	for _, tc := range []struct {
		depth   int
		refused bool
	}{
		{3, false},
		{4, false},
		{5, true},
		{10000, true},
	} {
		data := nestedArrays(tc.depth)

		r, err := NewReader(data, opts)
		if err != nil {
			t.Fatal(err)
		}
		skipErr := r.Skip()
		r.Reset(data)
		_, rawErr := r.ReadRaw()
		validateErr := Wire().WithMaxNestedLevels(4).Validate(data)

		for name, got := range map[string]error{"Skip": skipErr, "ReadRaw": rawErr, "Validate": validateErr} {
			if tc.refused {
				if !errors.Is(got, ErrLimitExceeded) {
					t.Errorf("depth %d: %s = %v, want ErrLimitExceeded", tc.depth, name, got)
				}
			} else if got != nil {
				t.Errorf("depth %d: %s = %v, want it accepted", tc.depth, name, got)
			}
		}
	}
}

// The header API reads one head and returns; the walk over the container is the
// caller's loop, so its depth is the caller's to bound. This pins that, because
// it differs from Decoder and the difference must not drift silently.
func TestTheHeaderAPILeavesNestingToTheCaller(t *testing.T) {
	data := nestedArrays(1000)
	opts := DecoderOptions{MaxNestedLevels: 4, MaxInputBytes: 1 << 20}

	r, err := NewReader(data, opts)
	if err != nil {
		t.Fatal(err)
	}
	levels := 0
	for {
		if _, _, err := r.ReadArrayHeader(); err != nil {
			break
		}
		levels++
	}
	if levels != 1000 {
		t.Errorf("ReadArrayHeader stopped after %d levels, want all 1000 -- it is documented as not bounding depth", levels)
	}

	// Decoder does bound it, from the same options.
	d, err := NewDecoder(bytes.NewReader(data), opts)
	if err != nil {
		t.Fatal(err)
	}
	tokens := 0
	var tokenErr error
	for {
		if _, tokenErr = d.ReadToken(); tokenErr != nil {
			break
		}
		tokens++
	}
	if !errors.Is(tokenErr, ErrLimitExceeded) {
		t.Errorf("Decoder.ReadToken = %v after %d tokens, want ErrLimitExceeded", tokenErr, tokens)
	}

	// And the boundary the documentation points untrusted input at does refuse,
	// once it is narrowed to something a schema would meet. The profile default
	// is a stack safety net, so it accepts this depth on purpose.
	if err := Wire().Validate(data); err != nil {
		t.Errorf("Wire().Validate = %v, want 1000 levels accepted under the default safety net", err)
	}
	if err := Wire().WithMaxNestedLevels(4).Validate(data); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("a narrowed wire profile = %v, want ErrLimitExceeded", err)
	}
}

// A foreign type inside a foreign type inside a foreign type. Encoding composes
// because each method appends into the buffer its parent is already building;
// decoding composes because ReadRaw hands each one its own bytes, at depth.
// Neither costs an allocation.
type nestedInner struct{ A, B int64 }

func (v nestedInner) AppendCBORTo(dst []byte) []byte {
	dst = AppendArrayHeader(dst, 2)
	dst = AppendInt(dst, v.A)
	return AppendInt(dst, v.B)
}

func (v *nestedInner) DecodeCBORFrom(data []byte) error {
	r := ReaderOver(data, DecoderOptions{})
	n, _, err := r.ReadArrayHeader()
	if err != nil {
		return err
	}
	if n != 2 {
		return ErrUnexpectedToken
	}
	if v.A, err = r.ReadInt(); err != nil {
		return err
	}
	v.B, err = r.ReadInt()
	return err
}

type nestedMiddle struct {
	Tag uint32
	In  nestedInner
}

func (v nestedMiddle) AppendCBORTo(dst []byte) []byte {
	dst = AppendArrayHeader(dst, 2)
	dst = AppendUint(dst, uint64(v.Tag))
	return v.In.AppendCBORTo(dst)
}

func (v *nestedMiddle) DecodeCBORFrom(data []byte) error {
	r := ReaderOver(data, DecoderOptions{})
	n, _, err := r.ReadArrayHeader()
	if err != nil {
		return err
	}
	if n != 2 {
		return ErrUnexpectedToken
	}
	if v.Tag, err = r.ReadUint32(); err != nil {
		return err
	}
	raw, err := r.ReadRaw()
	if err != nil {
		return err
	}
	return v.In.DecodeCBORFrom(raw)
}

type nestedOuter struct {
	ID  uint32
	Mid nestedMiddle
	End fixed64
}

func (v nestedOuter) AppendCBORTo(dst []byte) []byte {
	dst = AppendArrayHeader(dst, 3)
	dst = AppendUint(dst, uint64(v.ID))
	dst = v.Mid.AppendCBORTo(dst)
	return v.End.AppendCBORTo(dst)
}

func (v *nestedOuter) DecodeCBORFrom(data []byte) error {
	r := ReaderOver(data, DecoderOptions{})
	n, _, err := r.ReadArrayHeader()
	if err != nil {
		return err
	}
	if n != 3 {
		return ErrUnexpectedToken
	}
	if v.ID, err = r.ReadUint32(); err != nil {
		return err
	}
	raw, err := r.ReadRaw()
	if err != nil {
		return err
	}
	if err := v.Mid.DecodeCBORFrom(raw); err != nil {
		return err
	}
	if raw, err = r.ReadRaw(); err != nil {
		return err
	}
	return v.End.DecodeCBORFrom(raw)
}

func TestForeignTypesComposeAtDepth(t *testing.T) {
	want := nestedOuter{
		ID:  7,
		Mid: nestedMiddle{Tag: 42, In: nestedInner{A: -1, B: 1 << 40}},
		End: -99,
	}
	encoded := want.AppendCBORTo(nil)

	if err := Wire().Validate(encoded); err != nil {
		t.Fatalf("the wire profile refused a three-level message: %v (%x)", err, encoded)
	}

	var got nestedOuter
	if err := got.DecodeCBORFrom(encoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != want {
		t.Fatalf("round trip gave %+v, want %+v", got, want)
	}
	if again := got.AppendCBORTo(nil); string(again) != string(encoded) {
		t.Errorf("re-encoded to %x, want %x", again, encoded)
	}

	buf := make([]byte, 0, 64)
	if allocs := testingAllocs(func() { buf = want.AppendCBORTo(buf[:0]) }); allocs != 0 {
		t.Errorf("encoding three levels allocated %v times, want 0", allocs)
	}
	var out nestedOuter
	if allocs := testingAllocs(func() {
		if err := out.DecodeCBORFrom(encoded); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Errorf("decoding three levels allocated %v times, want 0", allocs)
	}
}
