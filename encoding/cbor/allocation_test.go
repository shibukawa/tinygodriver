package cbor

import (
	"bytes"
	"runtime"
	"testing"
)

// A CBOR length prefix arrives before the bytes it describes, and this decoder
// reads an unauthenticated passkey attestation. Reserving the declared length up
// front turned a five-byte request into a megabyte of live heap.
func TestADeclaredLengthDoesNotBecomeAnAllocation(t *testing.T) {
	// Major type 2, ai=26: a byte string declaring 16 MiB, with no payload.
	head := []byte{0x5a, 0x01, 0x00, 0x00, 0x00}
	const budget = 32 << 20

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	const attempts = 200
	for range attempts {
		decoder, err := NewDecoder(bytes.NewReader(head), DecoderOptions{
			MaxInputBytes:      budget,
			MaxStringBytes:     budget,
			MaxRawMessageBytes: budget,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decoder.ReadRaw(); err == nil {
			t.Fatal("a byte string with no payload was accepted")
		}
	}
	runtime.ReadMemStats(&after)

	// 200 attempts once reserved 200 x 16 MiB. Anything near that is the old
	// behaviour; the bound here is loose enough not to be a flake and tight
	// enough to fail if the reservation comes back.
	allocated := after.TotalAlloc - before.TotalAlloc
	if limit := uint64(attempts) * (1 << 20); allocated > limit {
		t.Errorf("%d attempts allocated %d bytes, want under %d", attempts, allocated, limit)
	}
}

// The refusal still names the limit it hit, so a caller can tell an oversized
// item from a malformed one.
func TestALengthBeyondTheInputBudgetNamesTheLimit(t *testing.T) {
	// MaxStringBytes is left generous so that the input budget is the bound
	// actually under test rather than the string bound reaching it first.
	decoder, err := NewDecoder(bytes.NewReader([]byte{0x5a, 0x01, 0x00, 0x00, 0x00}), DecoderOptions{
		MaxInputBytes:  64,
		MaxStringBytes: 32 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decoder.ReadRaw(); err == nil {
		t.Fatal("want a refusal")
	} else if !bytes.Contains([]byte(err.Error()), []byte("input bytes")) {
		t.Errorf("error = %v, want it to name the input byte limit", err)
	}
}

// A string that does arrive is still returned whole.
func TestAnArrivingStringIsStillRead(t *testing.T) {
	decoder, err := NewDecoder(bytes.NewReader([]byte{0x43, 1, 2, 3}), DecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := decoder.ReadBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(value, []byte{1, 2, 3}) {
		t.Errorf("ReadBytes = %v, want [1 2 3]", value)
	}
}
