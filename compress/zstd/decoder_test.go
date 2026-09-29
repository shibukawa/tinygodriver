//go:build tinygo || force_tinygo_logic

package zstd

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
)

// decodeAllForTest decodes src both ways -- with DecodeAll, and through a
// Reader read in small pieces -- and fails the test if the two disagree on the
// content or the error. They share the frame logic but not the I/O around it,
// so every case here holds both to the same result.
func decodeAllForTest(t *testing.T, src []byte, options ...DecoderOption) ([]byte, error) {
	t.Helper()
	got, err := DecodeAll(nil, src, options...)
	streamed, streamErr := readInPieces(src, 777, options...)
	if err != streamErr {
		t.Errorf("DecodeAll reports %v; Reader reports %v", err, streamErr)
	} else if err == nil && !bytes.Equal(got, streamed) {
		t.Errorf("DecodeAll decoded %d bytes; Reader decoded %d different ones", len(got), len(streamed))
	}
	return got, err
}

// readInPieces decodes src through a Reader, piece bytes per Read.
func readInPieces(src []byte, piece int, options ...DecoderOption) ([]byte, error) {
	r, err := NewReader(bytes.NewReader(src), options...)
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

// Frames from the reference CLI, v1.5.7, so that `tinygo test` reaches the parts
// of the format only other encoders write; the host-Go tests generate far more
// on the fly, but TinyGo cannot run the CLI. The content is this package's
// encoder sources, concatenated, and all three frames carry XXH64 checksums.
//
//	zstd -6 --target-compressed-block-size=1024 src -o level6-1k-blocks.zst
//	zstd -19 src -o level19.zst
//	zstd -1 < src > level1-stdin.zst
//
// Small blocks are where the encoder reuses tables, so the first holds
// treeless literals, repeat-mode sequence tables and matches into earlier
// blocks. The second has level 19's deeper tables and -1 probabilities; the
// third, from stdin, states no content size and has blocks with over 16 KiB of
// literals, which take the five-byte literals header.
var (
	//go:embed testdata/decode/level6-1k-blocks.zst
	goldenSmallBlocks []byte
	//go:embed testdata/decode/level19.zst
	goldenLevel19 []byte
	//go:embed testdata/decode/level1-stdin.zst
	goldenStdin []byte
)

const (
	goldenSize   = 57705
	goldenSHA256 = "b8d5a98b82872d17a1c7a14fe59740c62f291ec49653bd1b155d04464bc2f4dc"
)

func TestDecodeReferenceFrames(t *testing.T) {
	for name, frame := range map[string][]byte{
		"level 6, 1 KiB blocks": goldenSmallBlocks,
		"level 19":              goldenLevel19,
		"level 1 from stdin":    goldenStdin,
	} {
		got, err := decodeAllForTest(t, frame)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		sum := sha256.Sum256(got)
		if len(got) != goldenSize || hex.EncodeToString(sum[:]) != goldenSHA256 {
			t.Errorf("%s: decoded %d bytes with SHA-256 %x, want %d bytes with %s",
				name, len(got), sum, goldenSize, goldenSHA256)
		}
	}
}

// TestDecodeOwnEncoder covers what only this package's encoder writes -- RLE
// literals and the direct Huffman weight representation -- across the same
// shapes the encoder's own round-trip test uses.
func TestDecodeOwnEncoder(t *testing.T) {
	rnd := rand.New(rand.NewSource(20260927))
	randomBytes := func(n int) []byte {
		b := make([]byte, n)
		rnd.Read(b)
		return b
	}
	var rows bytes.Buffer
	for i := range 3000 {
		fmt.Fprintf(&rows, "row %d: name=item-%d value=%d\n", i, i, i*7%1000)
	}
	cases := map[string][]byte{
		"empty":            nil,
		"one byte":         []byte("x"),
		"single run":       bytes.Repeat([]byte{'q'}, 5000),
		"run over a block": bytes.Repeat([]byte{'q'}, 300<<10),
		"random":           randomBytes(70000),
		"rows":             rows.Bytes(),
		"rows over blocks": bytes.Repeat(rows.Bytes(), 3),
		"one literal byte": []byte(strings.Repeat("abcabcabc", 50) + strings.Repeat("x", 40) + strings.Repeat("abcabcabc", 50)),
		"noise then match": append(randomBytes(3000), bytes.Repeat([]byte("abcabcabc"), 200)...),
	}
	for name, src := range cases {
		encoded, _, err := EncodeAll(src, WithETag(false))
		if err != nil {
			t.Errorf("%s: EncodeAll: %v", name, err)
			continue
		}
		got, err := decodeAllForTest(t, encoded)
		if err != nil {
			t.Errorf("%s: decode: %v", name, err)
			continue
		}
		if !bytes.Equal(got, src) {
			t.Errorf("%s: decoded %d bytes that differ from the %d encoded", name, len(got), len(src))
		}
	}

	// Streaming with Flush cuts blocks short, so the repeat offsets and the
	// output run on across many small blocks.
	src := bytes.Repeat([]byte("GET /items/42 HTTP/1.1\r\nHost: example.com\r\n\r\n"), 3000)
	var out bytes.Buffer
	z, err := NewWriter(&out, WithETag(false))
	if err != nil {
		t.Fatal(err)
		return
	}
	for rest := src; len(rest) > 0; {
		n := min(999, len(rest))
		if _, err := z.Write(rest[:n]); err != nil {
			t.Fatal(err)
			return
		}
		if err := z.Flush(); err != nil {
			t.Fatal(err)
			return
		}
		rest = rest[n:]
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
		return
	}
	got, err := decodeAllForTest(t, out.Bytes())
	if err != nil || !bytes.Equal(got, src) {
		t.Errorf("flushed stream: err %v, %d bytes decoded of %d", err, len(got), len(src))
	}
}

// Frame pieces for building frames by hand.
var magic = []byte{0x28, 0xb5, 0x2f, 0xfd}

func blockHeader(typ, size int, last bool) []byte {
	v := uint32(size)<<3 | uint32(typ)<<1
	if last {
		v |= 1
	}
	return []byte{byte(v), byte(v >> 8), byte(v >> 16)}
}

func concat(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

func checksumOf(p []byte) []byte {
	var x xxh64
	x.reset()
	x.write(p)
	return binary.LittleEndian.AppendUint32(nil, uint32(x.sum64()))
}

// TestDecodeFrameHeaders decodes header layouts no encoder at hand writes, and
// the frame-level failures, each against a frame built by hand.
func TestDecodeFrameHeaders(t *testing.T) {
	hello := []byte("hello")
	raw := concat(blockHeader(blockRaw, 5, true), hello)
	// Window descriptor 0: a 1 KiB window.
	cases := []struct {
		name  string
		frame []byte
		want  []byte
		err   error
	}{
		{"single segment, 1-byte size", concat(magic, []byte{0x20, 5}, raw), hello, nil},
		{"2-byte size, offset by 256", concat(magic, []byte{0x40, 0, 0, 0}, blockHeader(blockRLE, 256, true), []byte{'r'}), bytes.Repeat([]byte{'r'}, 256), nil},
		{"4-byte size", concat(magic, []byte{0x80, 0, 5, 0, 0, 0}, raw), hello, nil},
		{"8-byte size", concat(magic, []byte{0xc0, 0, 5, 0, 0, 0, 0, 0, 0, 0}, raw), hello, nil},
		{"dictionary ID 0 in one byte", concat(magic, []byte{0x01, 0, 0}, raw), hello, nil},
		{"dictionary ID 0 in four bytes", concat(magic, []byte{0x03, 0, 0, 0, 0, 0}, raw), hello, nil},
		{"dictionary ID in two bytes", concat(magic, []byte{0x02, 0, 7, 0}, raw), nil, ErrDictionaryRequired},
		{"checksum", concat(magic, []byte{0x04, 0}, raw, checksumOf(hello)), hello, nil},
		{"checksum mismatch", concat(magic, []byte{0x04, 0}, raw, checksumOf([]byte("hellO"))), nil, errChecksum},
		{"checksum missing", concat(magic, []byte{0x04, 0}, raw, []byte{1, 2}), nil, io.ErrUnexpectedEOF},
		{"content size short", concat(magic, []byte{0x20, 6}, raw), nil, errContentSize},
		{"content size exceeded", concat(magic, []byte{0x40, 0, 0, 0}, blockHeader(blockRaw, 300, true), make([]byte, 300)), nil, errContentSize},
		{"block beyond a one-segment window", concat(magic, []byte{0x20, 4}, raw), nil, errCorruptBlock},
		{"reserved header bit", concat(magic, []byte{0x08, 0}, raw), nil, errCorruptFrame},
		{"window beyond the limit", concat(magic, []byte{0x00, 14 << 3}, raw), nil, ErrWindowTooLarge},
		{"block beyond the window", concat(magic, []byte{0x00, 0}, blockHeader(blockRaw, 1025, true), make([]byte, 1025)), nil, errCorruptBlock},
		{"reserved block type", concat(magic, []byte{0x00, 0}, blockHeader(3, 1, true), []byte{0}), nil, errCorruptBlock},
		{"empty RLE and raw blocks", concat(magic, []byte{0x20, 0}, blockHeader(blockRLE, 0, false), []byte{'x'}, blockHeader(blockRaw, 0, true)), nil, nil},
		{"RLE block", concat(magic, []byte{0x00, 0}, blockHeader(blockRLE, 1000, true), []byte{'r'}), bytes.Repeat([]byte{'r'}, 1000), nil},
		{"truncated header", concat(magic, []byte{0xc0, 0, 5}), nil, io.ErrUnexpectedEOF},
		{"truncated block", concat(magic, []byte{0x00, 0}, blockHeader(blockRaw, 5, true), []byte("hel")), nil, io.ErrUnexpectedEOF},
		{"no last block", concat(magic, []byte{0x00, 0}, blockHeader(blockRaw, 5, false), hello), nil, io.ErrUnexpectedEOF},
		{"not a frame", []byte("GIF89a"), nil, errBadMagic},
		{"short magic", magic[:3], nil, io.ErrUnexpectedEOF},
		{"no input", nil, nil, nil},
		{"skippable frames around a frame", concat(
			[]byte{0x5a, 0x2a, 0x4d, 0x18, 3, 0, 0, 0}, []byte("abc"),
			magic, []byte{0x20, 5}, raw,
			[]byte{0x50, 0x2a, 0x4d, 0x18, 0, 0, 0, 0}), hello, nil},
		{"skippable frame cut short", []byte{0x50, 0x2a, 0x4d, 0x18, 9, 0, 0, 0, 1}, nil, io.ErrUnexpectedEOF},
		{"two frames", concat(magic, []byte{0x20, 5}, raw, magic, []byte{0x20, 5}, raw), []byte("hellohello"), nil},
	}
	for _, c := range cases {
		got, err := decodeAllForTest(t, c.frame)
		if !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.err)
			continue
		}
		if c.err == nil && !bytes.Equal(got, c.want) {
			t.Errorf("%s: decoded %q, want %q", c.name, got, c.want)
		}
		// The window limit is this decoder's policy, not a defect of the frame.
		if len(c.frame) > 0 {
			assertReferenceAgrees(t, c.name, c.frame, c.err == nil || c.err == ErrWindowTooLarge)
		}
	}
}

// sequenceFrame builds a single-block frame from a literals section and
// sequences, through the encoder's own section writer, so that a test can state
// Offset_Values and sequence counts no encoder would choose. The sequences'
// ofValue fields are used as they stand.
func sequenceFrame(t *testing.T, litSection []byte, seqs []sequence) []byte {
	t.Helper()
	z, err := NewWriter(io.Discard)
	if err != nil {
		t.Fatal(err)
		return nil
	}
	z.seqs = seqs
	body := z.appendSequences(bytes.Clone(litSection))
	return concat(frameHeader, blockHeader(blockCompressed, len(body), true), body)
}

// TestDecodeSequences drives the sequence executor through the repeat-offset
// rules and the bounds a match must respect.
func TestDecodeSequences(t *testing.T) {
	// Repeat slots start 1, 4, 8. With literals, values 1-3 name slots 1-3;
	// without, they name slots 2, 3 and slot 1 less one. The cases that
	// begin with an explicit offset of 3 leave the slots at 3, 1, 4.
	lits := []byte("abcdefghij")
	cases := []struct {
		name string
		seqs []sequence
		want string
		err  error
	}{
		{"explicit offset", []sequence{{litLen: 4, matchLen: 3, ofValue: 4 + 3}}, "abcdabcefghij", nil},
		{"slot 1 with literals", []sequence{{litLen: 4, matchLen: 3, ofValue: 1}}, "abcddddefghij", nil},
		{"slot 2 with literals", []sequence{{litLen: 4, matchLen: 3, ofValue: 2}}, "abcdabcefghij", nil},
		{"slot 3 with literals", []sequence{{litLen: 8, matchLen: 3, ofValue: 3}}, "abcdefghabcij", nil},
		{"slot 2 without literals", []sequence{{litLen: 4, matchLen: 3, ofValue: 3 + 3}, {litLen: 0, matchLen: 3, ofValue: 1}}, "abcdbcddddefghij", nil},
		{"slot 3 without literals", []sequence{{litLen: 8, matchLen: 3, ofValue: 3 + 3}, {litLen: 0, matchLen: 3, ofValue: 2}}, "abcdefghfghhfgij", nil},
		{"slot 1 less one", []sequence{{litLen: 4, matchLen: 3, ofValue: 3 + 3}, {litLen: 0, matchLen: 3, ofValue: 3}}, "abcdbcdcdcefghij", nil},
		{"slot 1 less one reaching zero", []sequence{{litLen: 4, matchLen: 3, ofValue: 1 + 3}, {litLen: 0, matchLen: 3, ofValue: 3}}, "", errCorruptOffset},
		{"overlapping match", []sequence{{litLen: 2, matchLen: 9, ofValue: 2 + 3}}, "abababababacdefghij", nil},
		{"offset before the frame", []sequence{{litLen: 2, matchLen: 3, ofValue: 3 + 3}}, "", errCorruptOffset},
		{"literal length beyond the literals", []sequence{{litLen: 11, matchLen: 3, ofValue: 1}}, "", errCorruptSequences},
	}
	for _, c := range cases {
		frame := sequenceFrame(t, appendRawLiterals(nil, lits), c.seqs)
		got, err := decodeAllForTest(t, frame)
		if !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.err)
			continue
		}
		if c.err == nil && string(got) != c.want {
			t.Errorf("%s: decoded %q, want %q", c.name, got, c.want)
		}
		assertReferenceAgrees(t, c.name, frame, c.err == nil)
	}

	// A block of literals alone ends its sequences section at the count.
	frame := concat(frameHeader, blockHeader(blockCompressed, 12, true), appendRawLiterals(nil, lits), []byte{0})
	if got, err := decodeAllForTest(t, frame); err != nil || !bytes.Equal(got, lits) {
		t.Errorf("no sequences: decoded %q, err %v", got, err)
	}
	assertReferenceAgrees(t, "no sequences", frame, true)

	// Literals sections no single block of this encoder produces: an RLE
	// run, and the two that reuse tables from a block that never came.
	rle := concat(appendLiteralHeader(nil, 10, literalsRLE), []byte{'z'})
	frame = sequenceFrame(t, rle, []sequence{{litLen: 2, matchLen: 3, ofValue: 1}})
	if got, err := decodeAllForTest(t, frame); err != nil || string(got) != strings.Repeat("z", 13) {
		t.Errorf("RLE literals: decoded %q, err %v", got, err)
	}
	assertReferenceAgrees(t, "RLE literals", frame, true)

	h := literalsTreeless | 10<<4 | 5<<14 // single stream, 10 literals from 5 bytes
	treeless := concat([]byte{byte(h), byte(h >> 8), byte(h >> 16)}, []byte{1, 2, 3, 4, 5})
	frame = sequenceFrame(t, treeless, []sequence{{litLen: 2, matchLen: 3, ofValue: 1}})
	if _, err := decodeAllForTest(t, frame); !errors.Is(err, errCorruptLiterals) {
		t.Errorf("treeless literals in the first block: err = %v, want %v", err, errCorruptLiterals)
	}
	assertReferenceAgrees(t, "treeless literals in the first block", frame, false)

	repeat := concat(magic, []byte{0x00, 0}, blockHeader(blockCompressed, 4, true), []byte{0, 1, 0xfc, 0x80})
	if _, err := decodeAllForTest(t, repeat); !errors.Is(err, errCorruptSequences) {
		t.Errorf("repeat-mode tables in the first block: err = %v, want %v", err, errCorruptSequences)
	}
	assertReferenceAgrees(t, "repeat-mode tables in the first block", repeat, false)

	// More than 0x7f00 sequences takes the three-byte count. Each here is one
	// literal and a three-byte match of it, so exactly a full block holds
	// 32768 of them.
	const n = maxBlockSize / 4
	lits = make([]byte, n)
	seqs := make([]sequence, n)
	want := make([]byte, 0, maxBlockSize)
	for i := range seqs {
		lits[i] = byte('a' + i%26)
		seqs[i] = sequence{litLen: 1, matchLen: 3, ofValue: 1}
		want = append(want, lits[i], lits[i], lits[i], lits[i])
	}
	frame = sequenceFrame(t, appendRawLiterals(nil, lits), seqs)
	if frame[len(frameHeader)+3+literalHeaderSize(len(lits))+len(lits)] != 255 {
		t.Fatal("the sequence count did not take the three-byte form")
		return
	}
	got, err := decodeAllForTest(t, frame)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("0x7f00+ sequences: err %v, %d bytes decoded of %d", err, len(got), len(want))
	}
	assertReferenceAgrees(t, "0x7f00+ sequences", frame, true)
}

// TestDecodeMalformedDoesNotPanic cuts and flips valid frames. Every result
// must be an error or some output; none may panic, and a cut frame must fail.
func TestDecodeMalformedDoesNotPanic(t *testing.T) {
	var rows bytes.Buffer
	for i := range 4000 {
		fmt.Fprintf(&rows, "row %d: name=item-%d value=%d\n", i, i, i*7%1000)
	}
	var frames [][]byte
	for _, src := range [][]byte{rows.Bytes()[:3000], rows.Bytes()} {
		encoded, _, err := EncodeAll(src, WithETag(false))
		if err != nil {
			t.Fatal(err)
			return
		}
		frames = append(frames, encoded)
	}

	for _, frame := range frames {
		step := max(1, len(frame)/500)
		for n := 0; n < len(frame); n += step {
			if _, err := decodeAllForTest(t, frame[:n]); err == nil && n > 0 {
				t.Errorf("frame cut to %d of %d bytes decoded without error", n, len(frame))
			}
		}
	}

	rnd := rand.New(rand.NewSource(1))
	for _, frame := range frames {
		for range 2000 {
			bad := bytes.Clone(frame)
			for range 1 + rnd.Intn(3) {
				bad[rnd.Intn(len(bad))] ^= byte(1 << rnd.Intn(8))
			}
			decodeAllForTest(t, bad)
		}
	}
}
