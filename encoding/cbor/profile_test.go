package cbor

import (
	"bytes"
	"errors"
	"strings"
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
			if err := wireProfile.Validate(tc.data, defaultOpts()); !errors.Is(err, tc.want) {
				t.Errorf("wireProfile.Validate(%x, defaultOpts()) = %v, want %v", tc.data, err, tc.want)
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
			if err := wireProfile.Validate(tc.data, defaultOpts()); err != nil {
				t.Errorf("wireProfile.Validate(%x, defaultOpts()) = %v, want it accepted", tc.data, err)
			}
		})
	}
}

func TestWorldProfileAdmitsWhatWireRefuses(t *testing.T) {
	// A map in bytewise key order, with a tag inside it.
	data := []byte{0xa2, 0x01, 0xc1, 0x02, 0x03, 0x62, 'h', 'i'}
	if err := worldProfile.Validate(data, defaultOpts()); err != nil {
		t.Errorf("the world profile = %v, want it accepted", err)
	}
	if err := wireProfile.Validate(data, defaultOpts()); !errors.Is(err, ErrProfileViolation) {
		t.Errorf("the wire profile = %v, want a profile violation", err)
	}
}

func TestWorldProfileEnforcesBytewiseKeyOrder(t *testing.T) {
	// Keys 0x20 (-1) and 0x18 0x64 (100). Bytewise puts 0x18 first.
	bytewise := []byte{0xa2, 0x18, 0x64, 0x02, 0x20, 0x01}
	lengthFirst := []byte{0xa2, 0x20, 0x01, 0x18, 0x64, 0x02}

	if err := worldProfile.Validate(bytewise, defaultOpts()); err != nil {
		t.Errorf("bytewise order = %v, want it accepted", err)
	}
	if err := worldProfile.Validate(lengthFirst, defaultOpts()); !errors.Is(err, ErrMalformed) {
		t.Errorf("length-first order = %v, want it refused under the world profile", err)
	}
}

func TestWorldProfileStillRefusesFloats(t *testing.T) {
	if err := worldProfile.Validate([]byte{0xf9, 0x3e, 0x00}, defaultOpts()); !errors.Is(err, ErrProfileViolation) {
		t.Errorf("a float under the world profile = %v, want a violation", err)
	}
	if err := worldWithFloats.Validate([]byte{0xf9, 0x3e, 0x00}, defaultOpts()); err != nil {
		t.Errorf("AllowingFloats still refused it: %v", err)
	}
}

// A float has to be an error on both sides. Catching it only at encode leaves
// the receiver believing a message it should have rejected.
func TestFloatIsRefusedAtDecodeToo(t *testing.T) {
	data := []byte{0xf9, 0x3e, 0x00}

	t.Run("byte-slice reader", func(t *testing.T) {
		r, err := wireProfile.NewReader(data, defaultOpts())
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
		d, err := NewDecoder(bytes.NewReader(data), wireProfile.applyTo(defaultOpts()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.ReadToken(); !errors.Is(err, ErrFloatRefused) {
			t.Errorf("ReadToken = %v, want ErrFloatRefused", err)
		}
	})

	t.Run("validate", func(t *testing.T) {
		if err := Validate(data, wireProfile.applyTo(defaultOpts())); !errors.Is(err, ErrFloatRefused) {
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
			if err := wireProfile.ValidateAppended(dst, before, defaultOpts()); !errors.Is(err, tc.want) {
				t.Errorf("ValidateAppended = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateAppendedAcceptsAWellBehavedMethod(t *testing.T) {
	dst := AppendArrayHeader(nil, 1)
	before := len(dst)
	dst = fixed64(-1234).AppendCBORTo(dst)
	if err := wireProfile.ValidateAppended(dst, before, defaultOpts()); err != nil {
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

	err := wireProfile.ValidateAppended(dst, before, defaultOpts())
	var positioned *Error
	if !errors.As(err, &positioned) {
		t.Fatalf("err = %v, want a *cbor.Error", err)
	}
	if positioned.Offset != int64(before) {
		t.Errorf("offset = %d, want %d", positioned.Offset, before)
	}
}

// The zero value restricts nothing. It is the identity of the type, and the
// thing a caller reaches for to ask only "is this well-formed CBOR".
func TestTheZeroProfileRestrictsNothing(t *testing.T) {
	var any Profile
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"a map with text keys", []byte{0xa1, 0x63, 'k', 'e', 'y', 0x01}},
		{"a float", []byte{0xf9, 0x3e, 0x00}},
		{"a tag", []byte{0xc1, 0x01}},
		{"an indefinite array", []byte{0x9f, 0x01, 0xff}},
		{"an indefinite string", []byte{0x7f, 0x61, 'a', 0xff}},
		{"unsorted map keys", []byte{0xa2, 0x18, 0x64, 0x02, 0x01, 0x01}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := any.Validate(tc.data, defaultOpts()); err != nil {
				t.Errorf("the zero profile refused %s: %v", tc.name, err)
			}
		})
	}
	if err := any.Validate([]byte{0x1c}, defaultOpts()); !errors.Is(err, ErrMalformed) {
		t.Errorf("the zero profile accepted malformed input: %v", err)
	}
}

// Canonical is the restriction CTAP2 and COSE impose, which is the one this
// package's original consumer needs. Floats are not part of it.
func TestCanonicalIsTheCTAP2Restriction(t *testing.T) {
	lengthFirst := []byte{0xa2, 0x20, 0x01, 0x18, 0x64, 0x02}
	bytewise := []byte{0xa2, 0x18, 0x64, 0x02, 0x20, 0x01}

	if err := Canonical().Validate(lengthFirst, defaultOpts()); err != nil {
		t.Errorf("Canonical refused length-first keys: %v", err)
	}
	if err := Canonical().Validate(bytewise, defaultOpts()); !errors.Is(err, ErrMalformed) {
		t.Errorf("Canonical accepted bytewise keys: %v", err)
	}
	if err := Canonical().Validate([]byte{0x9f, 0x01, 0xff}, defaultOpts()); !errors.Is(err, ErrProfileViolation) {
		t.Errorf("Canonical accepted an indefinite array: %v", err)
	}
	if err := Canonical().Validate([]byte{0xf9, 0x3e, 0x00}, defaultOpts()); err != nil {
		t.Errorf("Canonical refused a float, which nothing in COSE forbids: %v", err)
	}
	// Deterministic is the same restriction with the other ordering.
	if err := Deterministic().Validate(bytewise, defaultOpts()); err != nil {
		t.Errorf("Deterministic refused bytewise keys: %v", err)
	}
	if err := Deterministic().Validate(lengthFirst, defaultOpts()); !errors.Is(err, ErrMalformed) {
		t.Errorf("Deterministic accepted length-first keys: %v", err)
	}
}

// A profile and a limit set are separate objects because they answer to
// different owners: one is the protocol both peers agreed on, the other is what
// this process is willing to read. Changing either must not look like changing
// the other.
func TestAProfileAndItsLimitsAreIndependent(t *testing.T) {
	// A COSE_Key-shaped map, three pairs, nested one deep.
	data := AppendMapHeader(nil, 1)
	data = AppendInt(data, 1)
	data = AppendMapHeader(data, 2)
	data = AppendInt(data, -1)
	data = AppendBytes(data, make([]byte, 32))
	data = AppendInt(data, -2)
	data = AppendBytes(data, make([]byte, 32))

	generous := DecoderOptions{MaxInputBytes: 1 << 20, MaxStringBytes: 1 << 10}
	stingy := DecoderOptions{MaxInputBytes: 1 << 20, MaxStringBytes: 16}

	// Same profile, different limits: the format verdict does not move.
	if err := Canonical().Validate(data, generous); err != nil {
		t.Errorf("generous limits: %v", err)
	}
	if err := Canonical().Validate(data, stingy); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("stingy limits = %v, want ErrLimitExceeded, not a profile verdict", err)
	}

	// Same limits, different profile: the limit verdict does not move.
	if err := wireProfile.Validate(data, generous); !errors.Is(err, ErrProfileViolation) {
		t.Errorf("the wire profile = %v, want a profile violation, not a limit one", err)
	}
	var any Profile
	if err := any.Validate(data, generous); err != nil {
		t.Errorf("the zero profile: %v", err)
	}
}

// The restriction a consumer needs is a struct literal. This is the whole of
// what moving Wire and World out of this package costs their owner.
func TestAConsumerNamesItsOwnRestriction(t *testing.T) {
	mine := Profile{
		Name:             "mine",
		RejectMaps:       true,
		RejectFloats:     true,
		RejectIndefinite: true,
	}
	if err := mine.Validate([]byte{0x83, 0x01, 0x02, 0x03}, defaultOpts()); err != nil {
		t.Errorf("an array: %v", err)
	}
	err := mine.Validate([]byte{0xa1, 0x01, 0x02}, defaultOpts())
	if !errors.Is(err, ErrProfileViolation) {
		t.Errorf("a map = %v, want a profile violation", err)
	}
	if !strings.Contains(err.Error(), "the mine profile") {
		t.Errorf("the refusal is %q, want it to name the profile", err)
	}
}

// An indefinite length is only meaningful on a string, an array or a map. On an
// integer or a tag it is a malformed head whatever the profile permits, and a
// profile that permits indefinite lengths is permitting the legal ones rather
// than waiving well-formedness.
//
// The fuzzer found this: while every profile refused indefinite lengths
// outright the check was unreachable, and the zero profile made it reachable.
func TestAnIndefiniteHeadOnAScalarIsMalformedUnderEveryProfile(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"an unsigned integer", []byte{0x1f}},
		{"a negative integer", []byte{0x3f}},
		{"a tag", []byte{0xdf}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range []Profile{{}, Canonical(), Deterministic(), wireProfile, worldProfile} {
				if err := p.Validate(tc.data, defaultOpts()); !errors.Is(err, ErrMalformed) {
					t.Errorf("the %q profile = %v, want ErrMalformed", p.Name, err)
				}
			}
			r := ReaderOver(tc.data, defaultOpts())
			if err := r.Skip(); !errors.Is(err, ErrMalformed) {
				t.Errorf("Skip = %v, want ErrMalformed", err)
			}
		})
	}
}
