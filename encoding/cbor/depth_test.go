package cbor

import (
	"errors"
	"testing"
)

// needsNestedLevels reports the smallest MaxNestedLevels under which data is
// walkable in one pass.
func needsNestedLevels(t *testing.T, data []byte) int {
	t.Helper()
	for n := 1; n <= 256; n++ {
		r, err := NewReader(data, DecoderOptions{MaxNestedLevels: n, MaxInputBytes: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		if r.Skip() == nil {
			return n
		}
	}
	t.Fatalf("%x is not walkable at any bound up to 256", data)
	return -1
}

// MaxNestedLevels counts nested containers, and a tag is one of them. Pinning
// this because "depth" is otherwise a word two people can read two ways, and
// the arithmetic on Profile.Validate depends on which.
func TestWhatCountsAsALevel(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want int
	}{
		{"a bare scalar", []byte{0x00}, 1},
		{"one array", []byte{0x81, 0x00}, 1},
		{"two arrays", []byte{0x81, 0x81, 0x00}, 2},
		{"two maps", []byte{0xa1, 0x01, 0xa1, 0x01, 0x00}, 2},
		{"a tag over a scalar", []byte{0xc1, 0x00}, 1},
		{"a tag over an array", []byte{0xc1, 0x81, 0x00}, 2},
		{"an array over a tag over an array", []byte{0x81, 0xc1, 0x81, 0x00}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsNestedLevels(t, tc.data); got != tc.want {
				t.Errorf("needs %d levels, want %d", got, tc.want)
			}
		})
	}
}

// snapshotEntry is [id, [x, y], [[slot, count], ...]].
func snapshotEntry() []byte {
	b := AppendArrayHeader(nil, 3)
	b = AppendUint(b, 42)
	b = AppendArrayHeader(b, 2)
	b = AppendInt(b, -100)
	b = AppendInt(b, 200)
	b = AppendArrayHeader(b, 2)
	for i := range 2 {
		b = AppendArrayHeader(b, 2)
		b = AppendUint(b, uint64(i))
		b = AppendUint(b, 7)
	}
	return b
}

// snapshotDoc is [tick, [entry, ...]].
func snapshotDoc() []byte {
	e := snapshotEntry()
	b := AppendArrayHeader(nil, 2)
	b = AppendUint(b, 1234)
	b = AppendArrayHeader(b, 3)
	for range 3 {
		b = append(b, e...)
	}
	return b
}

// patchOver is [base, [[op, [path...], value], ...]], the shape a delta or a
// patch takes: it carries a subtree of the document it describes, so it is
// always deeper than that document.
func patchOver(value []byte) []byte {
	b := AppendArrayHeader(nil, 2)
	b = AppendUint(b, 1234)
	b = AppendArrayHeader(b, 2)
	for range 2 {
		b = AppendArrayHeader(b, 3)
		b = AppendUint(b, 1)
		b = AppendArrayHeader(b, 3)
		b = AppendUint(b, 1)
		b = AppendUint(b, 0)
		b = AppendUint(b, 2)
		b = append(b, value...)
	}
	return b
}

// A profile has to be closed under the envelopes it is expected to carry. A
// bound that only just fits a message refuses every patch of one, which is a
// failure that shows up the first time a delta is generated and not before.
func TestAProfileMustHoldPatchesOfItsOwnMessages(t *testing.T) {
	snapshot := snapshotDoc()
	patch := patchOver(snapshot)
	patchOfPatch := patchOver(patchOver(snapshotEntry()))

	snapshotDepth := needsNestedLevels(t, snapshot)
	patchDepth := needsNestedLevels(t, patch)
	if envelope := patchDepth - snapshotDepth; envelope != 3 {
		t.Errorf("the patch envelope adds %d levels, want 3 -- the documented arithmetic on Profile.Validate says three", envelope)
	}

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"a snapshot", snapshot},
		{"a patch of it", patch},
		{"a patch of that patch", patchOfPatch},
	} {
		if err := wireProfile.Validate(tc.data, defaultOpts()); err != nil {
			t.Errorf("the wire profile refused %s at %d levels: %v", tc.name, needsNestedLevels(t, tc.data), err)
		}
		if err := worldProfile.Validate(tc.data, defaultOpts()); err != nil {
			t.Errorf("the world profile refused %s: %v", tc.name, err)
		}
	}
}

// A narrowed bound is a decoder setting now, not a profile property, so an
// envelope over a message needs a wider setting rather than a wider profile.
// The two objects stay separate: the same profile, different limits.
func TestAnEnvelopeBoundIsADecoderSetting(t *testing.T) {
	const schemaDepth = 12
	tight := DecoderOptions{MaxNestedLevels: schemaDepth}
	envelope := DecoderOptions{MaxNestedLevels: schemaDepth + 3}

	deep := nestedArrays(schemaDepth + 2)
	if err := wireProfile.Validate(deep, tight); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("the tight bound accepted %d levels, want a refusal", schemaDepth+2)
	}
	if err := wireProfile.Validate(deep, envelope); err != nil {
		t.Errorf("the envelope bound refused what it was widened for: %v", err)
	}
}

// Validate sums the depths; reading through ReadRaw takes the larger of them,
// because a captured item is measured from zero. A caller that validates before
// decoding needs the sum, and one that only decodes does not.
func TestReadRawMeasuresACapturedItemFromZero(t *testing.T) {
	child := nestedArrays(6)
	envelope := AppendArrayHeader(nil, 1)
	envelope = append(envelope, child...)

	if got, want := needsNestedLevels(t, envelope), 7; got != want {
		t.Fatalf("the envelope needs %d levels as one document, want %d", got, want)
	}

	opts := DecoderOptions{MaxNestedLevels: 6, MaxInputBytes: 1 << 20}

	whole, err := NewReader(envelope, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := whole.Skip(); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("walking the whole envelope at 6 = %v, want ErrLimitExceeded", err)
	}

	field, err := NewReader(envelope, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := field.ReadArrayHeader(); err != nil {
		t.Fatalf("the envelope header at 6: %v", err)
	}
	raw, err := field.ReadRaw()
	if err != nil {
		t.Fatalf("ReadRaw of a 6-level child at a bound of 6 = %v, want it captured: the depth spent reaching it must not count against it", err)
	}
	if len(raw) != len(child) {
		t.Errorf("captured %d bytes, want %d", len(raw), len(child))
	}
}
