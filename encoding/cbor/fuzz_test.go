//go:build !tinygo

package cbor

import (
	"bytes"
	"testing"
)

func FuzzValidate(f *testing.F) {
	for _, seed := range [][]byte{{0x00}, {0x80}, {0xa0}, {0xff}, {0x9f, 0x01, 0xff}, {0xa1, 0x01, 0x02}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = Validate(data, DecoderOptions{MaxInputBytes: 4096, MaxRawMessageBytes: 4096, RejectDuplicateMapKeys: true})
	})
}

// The byte-slice surface reads the same untrusted input the streaming decoder
// does, and it is the half that borrows rather than copies. What is checked
// here is not just the absence of a panic but the agreement between the
// entry points: a profile that accepts an item must describe an item that Skip
// consumes exactly, and a captured raw item must be a prefix of what was read.
func FuzzReaderSurface(f *testing.F) {
	for _, seed := range [][]byte{
		{0x00}, {0x80}, {0xa0}, {0xff},
		{0x9f, 0x01, 0xff},
		{0xa1, 0x01, 0x02},
		{0x84, 0x19, 0x04, 0xd2, 0x20, 0x00, 0x03},
		{0x7f, 0x61, 'a', 0x61, 'b', 0xff},
		{0xc1, 0x1a, 0x51, 0x4b, 0x67, 0xb0},
		{0xf9, 0x3e, 0x00},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, profile := range []Profile{wireProfile, worldProfile, worldWithFloats, Canonical(), Deterministic(), {}} {
			opts := defaultOpts()
			accepted := profile.Validate(data, opts) == nil

			r := profile.ReaderOver(data, opts)
			skipErr := r.Skip()
			if accepted {
				if skipErr != nil {
					t.Fatalf("%s accepted %x but Skip refused it: %v", profile.Name, data, skipErr)
				}
				if !r.Done() {
					t.Fatalf("%s accepted %x but Skip left %d bytes", profile.Name, data, r.Remaining())
				}
			}

			r.Reset(data)
			raw, rawErr := r.ReadRaw()
			if rawErr == nil {
				if len(raw) > len(data) || !bytes.Equal(raw, data[:len(raw)]) {
					t.Fatalf("ReadRaw returned %x, which is not a prefix of %x", raw, data)
				}
				if r.Offset() != len(raw) {
					t.Fatalf("ReadRaw returned %d bytes but consumed %d", len(raw), r.Offset())
				}
			}
			if accepted && rawErr != nil {
				t.Fatalf("%s accepted %x but ReadRaw refused it: %v", profile.Name, data, rawErr)
			}
		}
	})
}

// Whatever the encoder emits, the profile it was configured for must accept.
func FuzzRoundTripUnderProfile(f *testing.F) {
	f.Add(uint32(1234), int64(-1), int64(0), uint32(3))
	f.Fuzz(func(t *testing.T, tick uint32, x, y int64, buttons uint32) {
		dst := AppendArrayHeader(nil, 4)
		dst = AppendUint(dst, uint64(tick))
		dst = AppendInt(dst, x)
		dst = AppendInt(dst, y)
		dst = AppendUint(dst, uint64(buttons))

		if err := wireProfile.Validate(dst, defaultOpts()); err != nil {
			t.Fatalf("the wire profile refused what the append path produced: %v (%x)", err, dst)
		}

		r := ReaderOver(dst, DecoderOptions{})
		n, indefinite, err := r.ReadArrayHeader()
		if err != nil || indefinite || n != 4 {
			t.Fatalf("ReadArrayHeader = %d, %v, %v", n, indefinite, err)
		}
		if got, err := r.ReadUint32(); err != nil || got != tick {
			t.Fatalf("tick = %d, %v, want %d", got, err, tick)
		}
		if got, err := r.ReadInt(); err != nil || got != x {
			t.Fatalf("x = %d, %v, want %d", got, err, x)
		}
		if got, err := r.ReadInt(); err != nil || got != y {
			t.Fatalf("y = %d, %v, want %d", got, err, y)
		}
		if got, err := r.ReadUint32(); err != nil || got != buttons {
			t.Fatalf("buttons = %d, %v, want %d", got, err, buttons)
		}
		if !r.Done() {
			t.Fatalf("%d bytes left after decoding", r.Remaining())
		}
	})
}
