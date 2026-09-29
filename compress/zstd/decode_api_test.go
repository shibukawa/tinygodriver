// The decoding API as a caller sees it. This file has no build tag: it runs
// against klauspost on host Go and against the TinyGo decoder under TinyGo or
// force_tinygo_logic, so each case pins down behaviour both backends promise.

package zstd_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"testing"

	"github.com/shibukawa/tinygodriver/compress/zstd"
)

// readAll decompresses frame through a Reader, piece bytes per Read.
func readAll(frame []byte, piece int, options ...zstd.DecoderOption) ([]byte, error) {
	r, err := zstd.NewReader(bytes.NewReader(frame), options...)
	if err != nil {
		return nil, err
	}
	var out []byte
	buf := make([]byte, piece)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

// decodeBoth decompresses frame with DecodeAll and through a Reader, and
// reports a failure unless both give want, or both fail with wantErr.
func decodeBoth(t *testing.T, name string, frame, want []byte, wantErr error, options ...zstd.DecoderOption) {
	t.Helper()
	got, err := zstd.DecodeAll(nil, frame, options...)
	if !errors.Is(err, wantErr) {
		t.Errorf("%s: DecodeAll: err = %v, want %v", name, err, wantErr)
	} else if wantErr == nil && !bytes.Equal(got, want) {
		t.Errorf("%s: DecodeAll: %d bytes differ from the %d expected", name, len(got), len(want))
	}
	got, err = readAll(frame, 1000, options...)
	if !errors.Is(err, wantErr) {
		t.Errorf("%s: Reader: err = %v, want %v", name, err, wantErr)
	} else if wantErr == nil && !bytes.Equal(got, want) {
		t.Errorf("%s: Reader: %d bytes differ from the %d expected", name, len(got), len(want))
	}
}

func payload(size int) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, `{"id":%d,"name":"item-%d","price":%d}`+"\n", i, i, i*37%1000)
	}
	return b.Bytes()[:size]
}

func encode(t *testing.T, src []byte) []byte {
	t.Helper()
	encoded, _, err := zstd.EncodeAll(src, zstd.WithETag(false))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestDecodeRoundTrip(t *testing.T) {
	random := make([]byte, 200<<10)
	rand.New(rand.NewSource(1)).Read(random)
	for name, src := range map[string][]byte{
		"empty content":  {},
		"small":          []byte("hello, zstd"),
		"several blocks": payload(400 << 10),
		"random":         random,
	} {
		decodeBoth(t, name, encode(t, src), src, nil)
		// One byte per Read, which drains every block a byte at a time.
		if len(src) < 5000 {
			if got, err := readAll(encode(t, src), 1); err != nil || !bytes.Equal(got, src) {
				t.Errorf("%s: one byte per Read: err %v, %d bytes", name, err, len(got))
			}
		}
	}
}

// Hand-built frame pieces. Window descriptor 0 is a 1 KiB window.
var (
	magic         = []byte{0x28, 0xb5, 0x2f, 0xfd}
	helloRawBlock = []byte{0x59, 0x00, 0x00, 'h', 'e', 'l', 'l', 'o', ',', ' ', 'z', 's', 't', 'd'} // raw, last, 11 bytes
)

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestDecodeFrames(t *testing.T) {
	hello := []byte("hello, zstd")
	// Single segment, a one-byte content size, and an XXH64 checksum whose low
	// 32 bits come from github.com/cespare/xxhash/v2.
	checksummed := cat(magic, []byte{0x24, 11}, helloRawBlock, []byte{0x78, 0x96, 0x21, 0x3c})
	badChecksum := bytes.Clone(checksummed)
	badChecksum[len(badChecksum)-1] ^= 1
	a, b := payload(3000), payload(5000)[3000:]

	cases := []struct {
		name  string
		frame []byte
		want  []byte
		err   error
	}{
		{"checksum", checksummed, hello, nil},
		{"checksum mismatch", badChecksum, nil, zstd.ErrCorrupt},
		{"frames and skippable frames", cat(encode(t, a), []byte{0x5f, 0x2a, 0x4d, 0x18, 2, 0, 0, 0, 'x', 'y'}, encode(t, b)), cat(a, b), nil},
		{"no input", nil, nil, nil},
		{"not zstd", []byte("GIF89a, not a frame"), nil, zstd.ErrCorrupt},
		{"reserved block type", cat(magic, []byte{0x00, 0x00, 0x07, 0x00, 0x00, 0x00}), nil, zstd.ErrCorrupt},
		{"16 MiB window", cat(magic, []byte{0x00, 14 << 3}, helloRawBlock), nil, zstd.ErrWindowTooLarge},
		{"dictionary", cat(magic, []byte{0x01, 0x00, 7}, helloRawBlock), nil, zstd.ErrDictionaryRequired},
		{"trailing bytes", cat(checksummed, []byte{0x28, 0xb5}), nil, io.ErrUnexpectedEOF},
	}
	for _, c := range cases {
		decodeBoth(t, c.name, c.frame, c.want, c.err)
	}
	// The window limit is the caller's to raise.
	decodeBoth(t, "16 MiB window, allowed", cat(magic, []byte{0x00, 14 << 3}, helloRawBlock), hello, nil,
		zstd.WithMaxWindow(16<<20))
}

func TestDecodeTruncated(t *testing.T) {
	src := payload(300 << 10)
	frame := encode(t, src)
	for n := 1; n < len(frame); n += 1 + n/8 {
		decodeBoth(t, fmt.Sprintf("cut to %d of %d bytes", n, len(frame)), frame[:n], nil, io.ErrUnexpectedEOF)
		if t.Failed() {
			return
		}
	}
}

func TestDecodeMaxOutput(t *testing.T) {
	src := payload(10000)
	frame := encode(t, src)
	decodeBoth(t, "exactly the limit", frame, src, nil, zstd.WithMaxOutput(int64(len(src))))
	decodeBoth(t, "one byte over", frame, nil, zstd.ErrOutputTooLarge, zstd.WithMaxOutput(int64(len(src)-1)))

	// A Reader hands out everything up to the limit before it fails.
	got, err := readAll(frame, 999, zstd.WithMaxOutput(int64(len(src)-1)))
	if !errors.Is(err, zstd.ErrOutputTooLarge) || !bytes.Equal(got, src[:len(src)-1]) {
		t.Errorf("Reader over the limit: err %v after %d bytes, want ErrOutputTooLarge after %d",
			err, len(got), len(src)-1)
	}
}

func TestDecoderOptionsAreChecked(t *testing.T) {
	for name, option := range map[string]zstd.DecoderOption{
		"window below 1 KiB": zstd.WithMaxWindow(1<<10 - 1),
		"window above 1 GiB": zstd.WithMaxWindow(1<<30 + 1),
		"negative output":    zstd.WithMaxOutput(-1),
	} {
		if _, err := zstd.DecodeAll(nil, nil, option); err == nil {
			t.Errorf("%s: DecodeAll accepted it", name)
		}
		if _, err := zstd.NewReader(bytes.NewReader(nil), option); err == nil {
			t.Errorf("%s: NewReader accepted it", name)
		}
	}
	if _, err := zstd.DecodeAll(nil, nil, zstd.WithMaxWindow(1<<10), zstd.WithMaxOutput(0)); err != nil {
		t.Errorf("the smallest window and no output limit: %v", err)
	}
}

// failingReader returns its data and then err.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestReaderLifecycle(t *testing.T) {
	// A Reader built without a stream, as a pool builds one, fails until
	// Reset gives it one.
	idle, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("NewReader(nil): %v", err)
		return
	}
	if _, err := idle.Read(make([]byte, 10)); err == nil || err == io.EOF {
		t.Errorf("Read before Reset: %v, want an error", err)
	}

	// Construction reads nothing, so a stream that cannot be read at all
	// fails at the first Read, with its own error.
	broken := errors.New("broken stream")
	r, err := zstd.NewReader(&failingReader{err: broken})
	if err != nil {
		t.Fatalf("NewReader read from its stream: %v", err)
		return
	}
	if _, err := r.Read(make([]byte, 10)); !errors.Is(err, broken) {
		t.Errorf("Read from a broken stream: %v, want its own error", err)
	}

	// A stream failing mid-frame reports its error, not corruption.
	a := payload(50000)
	frameA := encode(t, a)
	if err := r.Reset(&failingReader{data: frameA[:len(frameA)/2], err: broken}); err != nil {
		t.Fatal(err)
		return
	}
	if _, err := io.ReadAll(r); !errors.Is(err, broken) {
		t.Errorf("stream failing mid-frame: %v, want its own error", err)
	}

	// Reset reuses the Reader for another stream.
	b := payload(70000)
	if err := r.Reset(bytes.NewReader(encode(t, b))); err != nil {
		t.Fatal(err)
		return
	}
	if got, err := io.ReadAll(r); err != nil || !bytes.Equal(got, b) {
		t.Errorf("after Reset: err %v, %d bytes of %d", err, len(got), len(b))
	}

	// Close ends use until Reset.
	if err := r.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, err := r.Read(make([]byte, 10)); !errors.Is(err, zstd.ErrClosed) {
		t.Errorf("Read after Close: %v, want ErrClosed", err)
	}
	if err := r.Reset(bytes.NewReader(frameA)); err != nil {
		t.Fatal(err)
		return
	}
	if got, err := io.ReadAll(r); err != nil || !bytes.Equal(got, a) {
		t.Errorf("Reset after Close: err %v, %d bytes of %d", err, len(got), len(a))
	}
	if err := r.Reset(nil); err == nil {
		t.Error("Reset accepted a nil reader")
	}
}
