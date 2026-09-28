//go:build force_tinygo_logic && !tinygo

// Decoding what other encoders write.
//
// The package's own encoder uses a small subset of the format, so round trips
// through it say little about a decoder. These tests decode the reference CLI
// and klauspost/compress across their levels and the options that change
// which parts of the format they reach for: FSE-compressed Huffman weights,
// treeless literals, repeat-mode tables, -1 probabilities, matches reaching
// into earlier blocks, windows from 1 KiB to beyond the decoder's limit, and
// frames with and without content sizes and checksums.

package zstd

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	kzstd "github.com/klauspost/compress/zstd"
)

// referenceSources is the content the reference encoders compress: realistic
// text and binary, plus synthetic shapes aimed at particular block types.
func referenceSources(t *testing.T) map[string][]byte {
	t.Helper()
	rnd := rand.New(rand.NewSource(20260927))
	sources := map[string][]byte{}

	files, err := filepath.Glob(filepath.Join(runtime.GOROOT(), "src", "net", "http", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go sources to compress: %v", err)
		return nil
	}
	var text []byte
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
			return nil
		}
		text = append(text, b...)
	}
	sources["go source"] = text

	exe, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
		return nil
	}
	if len(exe) > 3<<20 {
		exe = exe[1<<20 : 2<<20] // code and data, away from the header
	}
	sources["binary"] = exe

	var rows bytes.Buffer
	for i := range 6000 {
		fmt.Fprintf(&rows, `{"id":%d,"name":"item-%d","price":%d.%02d,"tags":["t%d","t%d"]}`+"\n",
			i, i, i*37%1000, i%100, i%7, i%11)
	}
	sources["json rows"] = rows.Bytes()

	random := make([]byte, 200<<10)
	rnd.Read(random)
	sources["random"] = random

	// Runs of every length around the RLE thresholds, separated by text, and
	// one run longer than a block.
	var runs bytes.Buffer
	for i := range 400 {
		runs.Write(bytes.Repeat([]byte{byte('a' + i%26)}, i%97))
		runs.WriteString("separator text between runs ")
	}
	runs.Write(bytes.Repeat([]byte{'z'}, 300<<10))
	sources["runs"] = runs.Bytes()

	// A chunk repeated further back than one block, so that matches must reach
	// into earlier blocks' output.
	chunk := make([]byte, 40<<10)
	rnd.Read(chunk)
	var far bytes.Buffer
	far.Write(chunk)
	far.Write(text[:300<<10])
	far.Write(chunk)
	far.Write(random[:50<<10])
	far.Write(chunk)
	sources["far repeats"] = far.Bytes()

	for _, n := range []int{0, 1, 2, 5, 31, 32, 33, 100, 1000, 1023, 1024, 1025, 4095, 4096} {
		sources[fmt.Sprintf("text %d", n)] = text[:n]
		sources[fmt.Sprintf("random %d", n)] = random[:n]
	}
	return sources
}

// assertDecodes decodes frame and compares it with want, reporting the first
// divergence, which is the datum that tells a dropped byte from a bad copy.
func assertDecodes(t *testing.T, frame, want []byte, maxWindow uint64) {
	t.Helper()
	got, err := newDecoder(maxWindow).decodeAll(nil, frame)
	if err != nil {
		t.Errorf("decode: %v (after %d of %d bytes)", err, len(got), len(want))
		return
	}
	if bytes.Equal(got, want) {
		return
	}
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			t.Errorf("decoded bytes diverge at offset %d of %d: got %#x, want %#x", i, len(want), got[i], want[i])
			return
		}
	}
	t.Errorf("decoded %d bytes, want %d", len(got), len(want))
}

// zstdCLI compresses src with the reference CLI. With viaFile the input is a
// file, so the frame states its content size and, when the content fits the
// window, is a single segment; through stdin the size is unknown.
func zstdCLI(t *testing.T, src []byte, viaFile bool, args ...string) []byte {
	t.Helper()
	args = append([]string{"-q", "-c"}, args...)
	var stdin *bytes.Reader
	if viaFile {
		path := filepath.Join(t.TempDir(), "src")
		if err := os.WriteFile(path, src, 0o600); err != nil {
			t.Fatal(err)
			return nil
		}
		args = append(args, path)
	} else {
		stdin = bytes.NewReader(src)
	}
	cmd := exec.Command("zstd", args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("zstd %s: %v", strings.Join(args, " "), err)
		return nil
	}
	return out
}

// assertReferenceAgrees checks the reference CLI's verdict on a frame: that it
// decodes, to the same bytes this package's decoder produces, when refOK, and
// that it fails otherwise. It is how hand-built frames, whose expected output
// is otherwise only the test author's reading of the format, are held to the
// reference implementation.
func assertReferenceAgrees(t *testing.T, name string, frame []byte, refOK bool) {
	t.Helper()
	path, err := exec.LookPath("zstd")
	if err != nil {
		return
	}
	cmd := exec.Command(path, "-q", "-d", "-c")
	cmd.Stdin = bytes.NewReader(frame)
	got, err := cmd.Output()
	switch {
	case refOK && err != nil:
		t.Errorf("%s: the reference decoder rejects the frame: %v", name, err)
	case !refOK && err == nil:
		t.Errorf("%s: the reference decoder accepts the frame", name)
	case refOK:
		ours, err := newDecoder(1<<30).decodeAll(nil, frame)
		if err != nil || !bytes.Equal(ours, got) {
			t.Errorf("%s: the reference decoder produced %d bytes; this one %d, err %v", name, len(got), len(ours), err)
		}
	}
}

func TestDecodeReferenceCLI(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd CLI is not installed")
	}
	sources := referenceSources(t)

	configs := [][]string{
		{"--fast=5"},
		{"-1"},
		{"-3"},
		{"-7"},
		{"-12"},
		{"-16"},
		{"-19"},
		{"--ultra", "-22"},
		{"-3", "--no-check"},
		{"-9", "--no-compress-literals"},
		{"-1", "--compress-literals"},
		// Many small blocks, which is where treeless literals and repeat-mode
		// tables pay off for the encoder.
		{"-6", "--target-compressed-block-size=1024"},
		{"-19", "--target-compressed-block-size=4096"},
		// A 1 KiB window, which also caps blocks at 1 KiB.
		{"--zstd=wlog=10"},
		{"--zstd=wlog=17,strat=5"},
		{"-5", "--long=23"},
	}
	for name, src := range sources {
		small := len(src) <= 4096
		if testing.Short() && !small && name != "far repeats" {
			continue
		}
		for _, args := range configs {
			for _, viaFile := range []bool{false, true} {
				if small && viaFile && args[0] != "-3" {
					continue // small inputs gain nothing from every level twice
				}
				t.Run(fmt.Sprintf("%s/%s/file=%v", name, strings.Join(args, " "), viaFile), func(t *testing.T) {
					t.Parallel()
					frame := zstdCLI(t, src, viaFile, args...)
					assertDecodes(t, frame, src, 1<<30)
				})
			}
		}
	}
}

// TestDecodeWindowLimit checks the limit is applied to what a frame declares,
// before anything is decoded or allocated.
func TestDecodeWindowLimit(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd CLI is not installed")
	}
	src := bytes.Repeat([]byte("window limit "), 1000)
	// From stdin the size is unknown, so level 22 declares its full 128 MiB.
	frame := zstdCLI(t, src, false, "--ultra", "-22")
	if _, err := newDecoder(8<<20).decodeAll(nil, frame); !errors.Is(err, errWindowTooLarge) {
		t.Fatalf("decode with an 8 MiB limit: %v, want errWindowTooLarge", err)
	}
	assertDecodes(t, frame, src, 128<<20)
}

func TestDecodeKlauspost(t *testing.T) {
	sources := referenceSources(t)
	type config struct {
		name string
		opts []kzstd.EOption
	}
	// Every level with its defaults, and every option that changes what
	// reaches the wire at the default level; the two do not interact.
	var configs []config
	for _, level := range []kzstd.EncoderLevel{
		kzstd.SpeedFastest, kzstd.SpeedDefault, kzstd.SpeedBetterCompression, kzstd.SpeedBestCompression,
	} {
		configs = append(configs, config{level.String(), []kzstd.EOption{kzstd.WithEncoderLevel(level)}})
	}
	configs = append(configs,
		config{"crc", []kzstd.EOption{kzstd.WithEncoderCRC(true)}},
		config{"no crc", []kzstd.EOption{kzstd.WithEncoderCRC(false)}},
		config{"single segment", []kzstd.EOption{kzstd.WithSingleSegment(true)}},
		config{"1 KiB window", []kzstd.EOption{kzstd.WithWindowSize(1 << 10)}},
		config{"all literals coded", []kzstd.EOption{kzstd.WithAllLitEntropyCompression(true)}},
		config{"no entropy coding", []kzstd.EOption{kzstd.WithNoEntropyCompression(true)}},
		config{"zero frames", []kzstd.EOption{kzstd.WithZeroFrames(true)}},
		// Padding is written as a skippable frame.
		config{"padded", []kzstd.EOption{kzstd.WithEncoderPadding(4096)}},
	)
	for _, c := range configs {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			enc, err := kzstd.NewWriter(nil, append(c.opts, kzstd.WithEncoderConcurrency(1))...)
			if err != nil {
				t.Fatal(err)
				return
			}
			for name, src := range sources {
				// EncodeAll states the content size; the streaming Writer
				// does not know it.
				assertDecodes(t, enc.EncodeAll(src, nil), src, 1<<30)
				var buf bytes.Buffer
				enc.Reset(&buf)
				if _, err := enc.Write(src); err != nil {
					t.Fatal(err)
					return
				}
				if err := enc.Close(); err != nil {
					t.Fatal(err)
					return
				}
				assertDecodes(t, buf.Bytes(), src, 1<<30)
				if t.Failed() {
					t.Fatalf("source %s", name)
					return
				}
			}
		})
	}
}

// FuzzDecode mutates valid frames and holds the decoder to klauspost's: no
// input may panic, and where both decode, they must agree. The two may still
// disagree on whether a frame is valid -- this decoder, like the reference,
// requires every bitstream to end exactly, where klauspost tolerates slack.
func FuzzDecode(f *testing.F) {
	f.Add(goldenSmallBlocks)
	f.Add(goldenStdin)
	for _, src := range [][]byte{
		nil,
		[]byte("hello, hello, hello"),
		bytes.Repeat([]byte("row 1: name=item value=7\n"), 200),
	} {
		encoded, _, err := EncodeAll(src, WithETag(false))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(encoded)
	}
	dec, err := kzstd.NewReader(nil, kzstd.WithDecoderConcurrency(1), kzstd.WithDecoderMaxMemory(64<<20))
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, frame []byte) {
		ours, err := newDecoder(8<<20).decodeAll(nil, frame)
		theirs, theirErr := dec.DecodeAll(frame, nil)
		if err == nil && theirErr == nil && !bytes.Equal(ours, theirs) {
			t.Fatalf("decoded %d bytes; klauspost decoded %d different ones", len(ours), len(theirs))
		}
	})
}
