package cbor

import (
	"bytes"
	"io"
	"testing"
)

var benchInput = playerInput{Tick: 1234, MoveX: -1, MoveY: 1, Buttons: 3}

// The append path: one buffer, reused, no children materialized.
func BenchmarkWireEncodeAppend(b *testing.B) {
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		buf = benchInput.AppendCBORTo(buf[:0])
	}
	_ = buf
}

// The shape the package shipped with: every child becomes a finished
// RawMessage before its parent can be written, and the encoder writes to an
// io.Writer. This is the same message through the old surface.
func BenchmarkWireEncodeMaterializing(b *testing.B) {
	var out bytes.Buffer
	encoder, err := NewEncoder(&out, EncoderOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		out.Reset()
		if err := encoder.WriteArray([]RawMessage{
			MarshalUint(uint64(benchInput.Tick)),
			MarshalInt(int64(benchInput.MoveX)),
			MarshalInt(int64(benchInput.MoveY)),
			MarshalUint(uint64(benchInput.Buttons)),
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWireDecodeReader(b *testing.B) {
	encoded := benchInput.AppendCBORTo(nil)
	r := ReaderOver(encoded, DecoderOptions{})
	var out playerInput
	b.ReportAllocs()
	for b.Loop() {
		r.Reset(encoded)
		if err := out.decodeFrom(&r); err != nil {
			b.Fatal(err)
		}
	}
}

// The incremental decoder over the same bytes, which is what the passkey path
// uses and what a byte-slice caller had to fall back on before.
func BenchmarkWireDecodeStreaming(b *testing.B) {
	encoded := benchInput.AppendCBORTo(nil)
	reader := bytes.NewReader(encoded)
	b.ReportAllocs()
	for b.Loop() {
		reader.Reset(encoded)
		d, err := NewDecoder(reader, DecoderOptions{})
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := d.ReadArray(); err != nil {
			b.Fatal(err)
		}
		for range 4 {
			if _, err := d.ReadToken(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkWireValidate(b *testing.B) {
	encoded := benchInput.AppendCBORTo(nil)
	profile := Wire()
	b.ReportAllocs()
	for b.Loop() {
		if err := profile.Validate(encoded); err != nil {
			b.Fatal(err)
		}
	}
}

// Skipping an unknown field is the world profile's tolerance of schema change,
// so it has to be cheaper than capturing one.
func BenchmarkSkipUnknownField(b *testing.B) {
	unknown := AppendArrayHeader(nil, 3)
	unknown = AppendText(unknown, "a field this build does not know")
	unknown = AppendUint(unknown, 12345)
	unknown = AppendBytes(unknown, make([]byte, 64))
	r := ReaderOver(unknown, DecoderOptions{})
	b.ReportAllocs()
	for b.Loop() {
		r.Reset(unknown)
		if err := r.Skip(); err != nil {
			b.Fatal(err)
		}
	}
}

func worldSnapshot() []byte {
	dst := AppendMapHeader(nil, 4)
	// bytewise key order, which is what the world profile enforces
	dst = AppendUint(dst, 1)
	dst = AppendUint(dst, 9999)
	dst = AppendUint(dst, 2)
	dst = AppendText(dst, "entity")
	dst = AppendUint(dst, 3)
	dst = AppendArrayHeader(dst, 8)
	for i := range 8 {
		dst = AppendInt(dst, int64(i)*7-3)
	}
	dst = AppendUint(dst, 4)
	dst = AppendBytes(dst, make([]byte, 128))
	return dst
}

func BenchmarkWorldValidate(b *testing.B) {
	data := worldSnapshot()
	profile := World()
	b.ReportAllocs()
	for b.Loop() {
		if err := profile.Validate(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWorldDecodeReader(b *testing.B) {
	data := worldSnapshot()
	r := World().ReaderOver(data)
	b.ReportAllocs()
	for b.Loop() {
		r.Reset(data)
		pairs, _, err := r.ReadMapHeader()
		if err != nil {
			b.Fatal(err)
		}
		for range pairs {
			if _, err := r.ReadUint(); err != nil {
				b.Fatal(err)
			}
			if err := r.Skip(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// The passkey path, unchanged: an attestation-shaped map read through the
// incremental decoder with duplicate key rejection on.
func BenchmarkUntrustedMapWithDuplicateRejection(b *testing.B) {
	data := worldSnapshot()
	opts := DecoderOptions{
		MaxInputBytes:          4096,
		MaxRawMessageBytes:     4096,
		RejectDuplicateMapKeys: true,
	}
	b.ReportAllocs()
	for b.Loop() {
		d, err := NewDecoder(bytes.NewReader(data), opts)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := d.ReadRaw(); err != nil && err != io.EOF {
			b.Fatal(err)
		}
	}
}
