package cbor

import (
	"bytes"
	"errors"
	"testing"
)

func TestWireProfileRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"a map", []byte{0xa1, 0x01, 0x02}, ErrProfileViolation},
		{"a tag", []byte{0xc1, 0x01}, ErrProfileViolation},
		{"a half float", []byte{0xf9, 0x3e, 0x00}, ErrProfileViolation},
		{"a double float", []byte{0xfb, 0x3f, 0xf8, 0, 0, 0, 0, 0, 0}, ErrProfileViolation},
		{"an indefinite array", []byte{0x9f, 0x01, 0xff}, ErrProfileViolation},
		{"an indefinite string", []byte{0x7f, 0x61, 'a', 0xff}, ErrProfileViolation},
		{"a nested map", []byte{0x81, 0xa1, 0x01, 0x02}, ErrProfileViolation},
		{"trailing bytes", []byte{0x01, 0x02}, ErrExtraneousData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Wire().Validate(tc.data); !errors.Is(err, tc.want) {
				t.Errorf("Wire().Validate(%x) = %v, want %v", tc.data, err, tc.want)
			}
		})
	}
}

func TestWireProfileAccepts(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"a fixed-order array", []byte{0x84, 0x19, 0x04, 0xd2, 0x20, 0x00, 0x03}},
		{"a nested array", []byte{0x82, 0x82, 0x01, 0x02, 0x03}},
		{"a byte string", []byte{0x43, 1, 2, 3}},
		{"a text string", []byte{0x62, 'h', 'i'}},
		{"a bare integer", []byte{0x01}},
		{"booleans and null", []byte{0x83, 0xf5, 0xf4, 0xf6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Wire().Validate(tc.data); err != nil {
				t.Errorf("Wire().Validate(%x) = %v, want it accepted", tc.data, err)
			}
		})
	}
}

func TestWorldProfileAdmitsWhatWireRefuses(t *testing.T) {
	// A map in bytewise key order, with a tag inside it.
	data := []byte{0xa2, 0x01, 0xc1, 0x02, 0x03, 0x62, 'h', 'i'}
	if err := World().Validate(data); err != nil {
		t.Errorf("World().Validate = %v, want it accepted", err)
	}
	if err := Wire().Validate(data); !errors.Is(err, ErrProfileViolation) {
		t.Errorf("Wire().Validate = %v, want a profile violation", err)
	}
}

func TestWorldProfileEnforcesBytewiseKeyOrder(t *testing.T) {
	// Keys 0x20 (-1) and 0x18 0x64 (100). Bytewise puts 0x18 first.
	bytewise := []byte{0xa2, 0x18, 0x64, 0x02, 0x20, 0x01}
	lengthFirst := []byte{0xa2, 0x20, 0x01, 0x18, 0x64, 0x02}

	if err := World().Validate(bytewise); err != nil {
		t.Errorf("bytewise order = %v, want it accepted", err)
	}
	if err := World().Validate(lengthFirst); !errors.Is(err, ErrMalformed) {
		t.Errorf("length-first order = %v, want it refused under the world profile", err)
	}
}

func TestWorldProfileStillRefusesFloats(t *testing.T) {
	if err := World().Validate([]byte{0xf9, 0x3e, 0x00}); !errors.Is(err, ErrProfileViolation) {
		t.Errorf("a float under the world profile = %v, want a violation", err)
	}
	if err := World().AllowingFloats().Validate([]byte{0xf9, 0x3e, 0x00}); err != nil {
		t.Errorf("AllowingFloats still refused it: %v", err)
	}
}

// A float has to be an error on both sides. Catching it only at encode leaves
// the receiver believing a message it should have rejected.
func TestFloatIsRefusedAtDecodeToo(t *testing.T) {
	data := []byte{0xf9, 0x3e, 0x00}

	t.Run("byte-slice reader", func(t *testing.T) {
		r, err := Wire().NewReader(data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.ReadFloat(); !errors.Is(err, ErrFloatRefused) {
			t.Errorf("ReadFloat = %v, want ErrFloatRefused", err)
		}
		r.Reset(data)
		if err := r.Skip(); !errors.Is(err, ErrFloatRefused) {
			t.Errorf("Skip past a float = %v, want ErrFloatRefused", err)
		}
	})

	t.Run("streaming decoder", func(t *testing.T) {
		d, err := NewDecoder(bytes.NewReader(data), Wire().DecoderOptions())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.ReadToken(); !errors.Is(err, ErrFloatRefused) {
			t.Errorf("ReadToken = %v, want ErrFloatRefused", err)
		}
	})

	t.Run("validate", func(t *testing.T) {
		if err := Validate(data, Wire().DecoderOptions()); !errors.Is(err, ErrFloatRefused) {
			t.Errorf("Validate = %v, want ErrFloatRefused", err)
		}
	})
}

// The interface cannot be enforced by the type system, so the profile has to
// check what a foreign implementation actually appended.
type leakyFloat struct{}

func (leakyFloat) AppendCBORTo(dst []byte) []byte { return AppendFloat(dst, 1.5) }

type leakyIndefinite struct{}

func (leakyIndefinite) AppendCBORTo(dst []byte) []byte {
	return append(dst, 0x9f, 0x01, 0xff)
}

type leakyNothing struct{}

func (leakyNothing) AppendCBORTo(dst []byte) []byte { return dst }

type leakyTwoItems struct{}

func (leakyTwoItems) AppendCBORTo(dst []byte) []byte { return append(dst, 0x01, 0x02) }

func TestValidateAppendedCatchesAForeignMethod(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    Appender
		want error
	}{
		{"a float", leakyFloat{}, ErrProfileViolation},
		{"an indefinite item", leakyIndefinite{}, ErrProfileViolation},
		{"nothing at all", leakyNothing{}, ErrTruncated},
		{"two items where one was promised", leakyTwoItems{}, ErrExtraneousData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := AppendArrayHeader(nil, 1)
			before := len(dst)
			dst = tc.v.AppendCBORTo(dst)
			if err := Wire().ValidateAppended(dst, before); !errors.Is(err, tc.want) {
				t.Errorf("ValidateAppended = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateAppendedAcceptsAWellBehavedMethod(t *testing.T) {
	dst := AppendArrayHeader(nil, 1)
	before := len(dst)
	dst = fixed64(-1234).AppendCBORTo(dst)
	if err := Wire().ValidateAppended(dst, before); err != nil {
		t.Errorf("ValidateAppended = %v, want it accepted", err)
	}
}

// The offset a violation reports has to be a position in the whole buffer, not
// in the fragment the foreign method wrote.
func TestValidateAppendedReportsAWholeBufferOffset(t *testing.T) {
	dst := AppendArrayHeader(nil, 2)
	dst = AppendUint(dst, 7)
	before := len(dst)
	dst = leakyFloat{}.AppendCBORTo(dst)

	err := Wire().ValidateAppended(dst, before)
	var positioned *Error
	if !errors.As(err, &positioned) {
		t.Fatalf("err = %v, want a *cbor.Error", err)
	}
	if positioned.Offset != int64(before) {
		t.Errorf("offset = %d, want %d", positioned.Offset, before)
	}
}
