package cbor

import (
	"errors"
	"strings"
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

// The nesting limit is a stack safety net set far past any schema, so the input
// that trips it is nested thousands deep. Describing the route to that would
// recurse thousands deep a second time and produce tens of kilobytes of
// "[0][0][0]" that says nothing, which is a second denial of service wearing
// the first one's error message.
func TestADeepRefusalDoesNotProduceADeepMessage(t *testing.T) {
	data := nestedArrays(200000)
	r, err := NewReader(data, DecoderOptions{MaxInputBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	err = r.Skip()
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("200000 levels = %v, want ErrLimitExceeded", err)
	}
	if n := len(err.Error()); n > 200 {
		t.Errorf("the refusal is %d bytes long, want it bounded: %.120s...", n, err.Error())
	}
	var positioned *Error
	if !errors.As(err, &positioned) {
		t.Fatalf("err = %v, want a *cbor.Error", err)
	}
	if positioned.Path != "" {
		t.Errorf("path = %q, want none: past a human depth the offset is the whole answer", positioned.Path)
	}
}

// A text key can be megabytes under the world profile. An error message is not
// the place to repeat one.
func TestALongKeyIsTruncatedInTheRoute(t *testing.T) {
	key := make([]byte, 4096)
	for i := range key {
		key[i] = 'k'
	}
	data := AppendMapHeader(nil, 1)
	data = AppendText(data, string(key))
	data = append(data, 0x1c) // reserved additional information, as the value

	err := World().Validate(data)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
	if n := len(err.Error()); n > 200 {
		t.Errorf("the refusal is %d bytes long, want it bounded", n)
	}
	var positioned *Error
	if !errors.As(err, &positioned) {
		t.Fatal("want a *cbor.Error")
	}
	if !strings.Contains(positioned.Path, "...") {
		t.Errorf("path = %q, want the key elided", positioned.Path)
	}
}

// The default nesting bound exists so that input this deep is refused rather
// than exhausting the stack. Measured, TinyGo faults at roughly forty-seven
// thousand levels on an 8 MiB stack, with a bare SIGSEGV and no message.
func TestTheDefaultNestingBoundIsAStackSafetyNet(t *testing.T) {
	if defaultMaxNestedLevels < 1000 {
		t.Fatalf("defaultMaxNestedLevels = %d, want a safety net rather than a budget", defaultMaxNestedLevels)
	}
	if defaultMaxNestedLevels > 20000 {
		t.Fatalf("defaultMaxNestedLevels = %d, want it well short of where TinyGo faults", defaultMaxNestedLevels)
	}
	for _, depth := range []int{defaultMaxNestedLevels - 1, defaultMaxNestedLevels + 1, 100000} {
		r, err := NewReader(nestedArrays(depth), DecoderOptions{MaxInputBytes: 1 << 30})
		if err != nil {
			t.Fatal(err)
		}
		err = r.Skip()
		if depth < defaultMaxNestedLevels {
			if err != nil {
				t.Errorf("depth %d = %v, want it accepted", depth, err)
			}
		} else if !errors.Is(err, ErrLimitExceeded) {
			t.Errorf("depth %d = %v, want ErrLimitExceeded", depth, err)
		}
	}
}
