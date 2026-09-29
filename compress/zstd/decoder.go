//go:build tinygo || force_tinygo_logic

// Decoding, for TinyGo: frames, blocks, and the state a frame's blocks share.
//
// The encoder in this package writes a small subset of RFC 8878, but a decoder
// takes frames from anywhere, so this one implements the whole format apart
// from dictionaries: every literals and sequence table mode, the repeat
// offsets across blocks, windows up to a configured limit, skippable frames,
// and content checksums. Every length and table on the wire is checked before
// it is used, since the input is not trusted.
//
// A frame is decoded in four steps -- beginFrame, then parseBlockHeader and
// appendBlock for each block, then endFrame -- which decodeAll drives over a
// byte slice and Reader over a stream. Both therefore apply the same checks.

package zstd

import (
	"encoding/binary"
	"io"
)

// Block_Type values.
const (
	blockRaw        = 0
	blockRLE        = 1
	blockCompressed = 2
)

const (
	frameMagic         = 0xfd2fb528
	skippableMagic     = 0x184d2a50 // the low four bits are free
	skippableMagicMask = 0xfffffff0

	// maxFrameHeader is the largest frame header after the magic number:
	// descriptor, window, a four-byte dictionary ID, an eight-byte size.
	maxFrameHeader = 14

	// maxFramePrealloc bounds how much of a declared Frame_Content_Size is
	// allocated before any of it is decoded. The field is the sender's claim,
	// and a few bytes of header must not buy megabytes of memory.
	maxFramePrealloc = 1 << 20
)

// corruption reports input that violates the format, naming where the decoder
// found it. It is a string type so that each value is a constant, and it
// matches ErrCorrupt under errors.Is.
type corruption string

func (c corruption) Error() string { return "zstd: corrupt input: " + string(c) }

func (c corruption) Is(target error) bool { return target == ErrCorrupt }

const (
	errBadMagic         corruption = "not a Zstandard frame"
	errCorruptFrame     corruption = "frame header"
	errCorruptBlock     corruption = "block header"
	errCorruptLiterals  corruption = "literals section"
	errCorruptSequences corruption = "sequences section"
	errCorruptTable     corruption = "FSE table description"
	errCorruptBitstream corruption = "entropy-coded bitstream"
	errCorruptOffset    corruption = "match offset"
	errContentSize      corruption = "content size differs from the frame header"
	errChecksum         corruption = "content checksum mismatch"
)

// frameParams is what a frame header declares.
type frameParams struct {
	windowSize     uint64
	contentSize    uint64
	hasContentSize bool
	checksum       bool
	dictID         uint32
}

// frameHeaderSize is the size of the frame header whose descriptor byte is
// fhd, the descriptor included, which is what a streaming reader needs to know
// before it can read the rest.
func frameHeaderSize(fhd byte) int {
	single := fhd&0x20 != 0
	n := 1 + [4]int{0, 1, 2, 4}[fhd&3] + [4]int{0, 2, 4, 8}[fhd>>6]
	if single && fhd>>6 == 0 {
		n++ // a one-byte content size
	}
	if !single {
		n++ // the window descriptor
	}
	return n
}

// parseFrameParams parses the frame header that follows the magic number,
// returning it and the bytes it took.
func parseFrameParams(src []byte) (frameParams, int, error) {
	var h frameParams
	if len(src) == 0 {
		return h, 0, io.ErrUnexpectedEOF
	}
	// The length comes first, as it must for a Reader, which cannot parse a
	// header before it has all of it; both then fail a cut-off header alike.
	fhd := src[0]
	n := frameHeaderSize(fhd)
	if len(src) < n {
		return h, 0, io.ErrUnexpectedEOF
	}
	if fhd&8 != 0 {
		return h, 0, errCorruptFrame // reserved bit
	}
	single := fhd&0x20 != 0
	h.checksum = fhd&4 != 0
	dictSize := [4]int{0, 1, 2, 4}[fhd&3]
	contentSize := n - 1 - dictSize
	if !single {
		contentSize--
	}

	pos := 1
	if !single {
		// An exponent and an eighths mantissa: 1 KiB up to 3.75 TiB.
		wd := src[pos]
		windowBase := uint64(1) << (10 + wd>>3)
		h.windowSize = windowBase + windowBase/8*uint64(wd&7)
		pos++
	}
	switch dictSize {
	case 1:
		h.dictID = uint32(src[pos])
	case 2:
		h.dictID = uint32(binary.LittleEndian.Uint16(src[pos:]))
	case 4:
		h.dictID = binary.LittleEndian.Uint32(src[pos:])
	}
	pos += dictSize
	h.hasContentSize = contentSize != 0
	switch contentSize {
	case 1:
		h.contentSize = uint64(src[pos])
	case 2:
		h.contentSize = uint64(binary.LittleEndian.Uint16(src[pos:])) + 256
	case 4:
		h.contentSize = uint64(binary.LittleEndian.Uint32(src[pos:]))
	case 8:
		h.contentSize = binary.LittleEndian.Uint64(src[pos:])
	}
	if single {
		// One segment: the whole content is the window.
		h.windowSize = h.contentSize
	}
	return h, n, nil
}

// decoder holds what the blocks of one frame share, plus working storage
// reused from frame to frame. A decoder is not safe for concurrent use.
type decoder struct {
	maxWindow uint64
	maxOutput int64 // 0 means no limit

	// The frame being decoded: its header, its window and the largest block
	// content that allows, and how much content it has produced.
	frame    frameParams
	window   int
	blockMax int
	produced uint64

	// The repeat offset slots, and whether the previous block's Huffman and
	// sequence tables exist for a treeless or repeat-mode block to reuse. All
	// three reset at each frame.
	rep       [3]uint32
	huffValid bool
	seqValid  bool

	huff        huffDecTable
	weightTable fseDecTable
	llTable     fseDecTable
	ofTable     fseDecTable
	mlTable     fseDecTable

	norm    [maxFSESymbols]int16
	weights [255]byte
	lits    []byte
	hash    xxh64
}

// configure applies resolved options; the constructors have already checked
// them, and the window was capped at maxWindowCeiling there.
func (d *decoder) configure(options decoderOptions) {
	d.maxWindow = uint64(options.maxWindow)
	d.maxOutput = options.maxOutput
}

// decodeAll appends the content of every frame in src to dst. Skippable frames
// are skipped; anything else that is not a whole frame is an error.
func (d *decoder) decodeAll(dst, src []byte) ([]byte, error) {
	origin := len(dst)
	for len(src) > 0 {
		if len(src) < 4 {
			return dst, io.ErrUnexpectedEOF
		}
		magic := binary.LittleEndian.Uint32(src)
		if magic&skippableMagicMask == skippableMagic {
			if len(src) < 8 {
				return dst, io.ErrUnexpectedEOF
			}
			size := binary.LittleEndian.Uint32(src[4:])
			if uint64(len(src)-8) < uint64(size) {
				return dst, io.ErrUnexpectedEOF
			}
			src = src[8+int(size):]
			continue
		}
		if magic != frameMagic {
			return dst, errBadMagic
		}
		var err error
		if dst, src, err = d.decodeFrame(dst, src[4:], int64(len(dst)-origin)); err != nil {
			return dst, err
		}
	}
	return dst, nil
}

// decodeFrame decodes the frame at the front of src, which starts after the
// magic number, and returns what follows it. before is how much content
// earlier frames produced, which counts against the output limit.
func (d *decoder) decodeFrame(dst, src []byte, before int64) ([]byte, []byte, error) {
	h, n, err := parseFrameParams(src)
	if err != nil {
		return dst, src, err
	}
	src = src[n:]
	if err := d.beginFrame(h); err != nil {
		return dst, src, err
	}
	if h.hasContentSize {
		// A frame that says it will exceed the limit is refused before it is
		// decoded; one that says otherwise is still held to it block by block.
		if d.maxOutput > 0 && h.contentSize > uint64(d.maxOutput-before) {
			return dst, src, ErrOutputTooLarge
		}
		want := int(min(h.contentSize, maxFramePrealloc))
		if cap(dst)-len(dst) < want {
			grown := make([]byte, len(dst), len(dst)+want)
			copy(grown, dst)
			dst = grown
		}
	}

	for last := false; !last; {
		if len(src) < 3 {
			return dst, src, io.ErrUnexpectedEOF
		}
		var typ, size int
		if typ, size, last, err = d.parseBlockHeader(src); err != nil {
			return dst, src, err
		}
		src = src[3:]
		payload := blockPayloadSize(typ, size)
		if len(src) < payload {
			return dst, src, io.ErrUnexpectedEOF
		}
		start := len(dst)
		if dst, err = d.appendBlock(dst, typ, size, src[:payload]); err != nil {
			return dst, src, err
		}
		src = src[payload:]
		if err := d.blockDone(dst[start:]); err != nil {
			return dst, src, err
		}
		if d.maxOutput > 0 && before+int64(d.produced) > d.maxOutput {
			return dst, src, ErrOutputTooLarge
		}
	}

	var sum []byte
	if h.checksum {
		if len(src) < 4 {
			return dst, src, io.ErrUnexpectedEOF
		}
		sum, src = src[:4], src[4:]
	}
	return dst, src, d.endFrame(sum)
}

// beginFrame checks a frame's header against what this decoder accepts and
// starts the per-frame state.
func (d *decoder) beginFrame(h frameParams) error {
	if h.dictID != 0 {
		return ErrDictionaryRequired
	}
	if h.windowSize > d.maxWindow {
		return ErrWindowTooLarge
	}
	d.frame = h
	d.window = int(h.windowSize)
	d.blockMax = min(d.window, maxBlockSize)
	d.produced = 0
	d.rep = [3]uint32{1, 4, 8}
	d.huffValid = false
	d.seqValid = false
	if h.checksum {
		d.hash.reset()
	}
	return nil
}

// parseBlockHeader reads the three-byte block header at the front of b.
func (d *decoder) parseBlockHeader(b []byte) (typ, size int, last bool, err error) {
	bh := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16
	typ, size, last = int(bh>>1&3), int(bh>>3), bh&1 != 0
	// The size is the block's content for raw and RLE blocks and its encoded
	// form for compressed ones; the format caps both the same.
	if typ == 3 || size > d.blockMax {
		return 0, 0, false, errCorruptBlock // 3 is reserved
	}
	return typ, size, last, nil
}

// blockPayloadSize is how many bytes follow a block header: one for an RLE
// block, its size otherwise.
func blockPayloadSize(typ, size int) int {
	if typ == blockRLE {
		return 1
	}
	return size
}

// appendBlock appends one block's content to out. payload is what follows the
// block header: a raw block's content, an RLE block's byte, or a compressed
// block. out must end with this frame's content so far, or at least the last
// window of it, since that is what matches copy from.
func (d *decoder) appendBlock(out []byte, typ, size int, payload []byte) ([]byte, error) {
	switch typ {
	case blockRaw:
		return append(out, payload...), nil
	case blockRLE:
		return appendRun(out, payload[0], size), nil
	default:
		reach := int(min(d.produced, uint64(d.window)))
		return d.decodeCompressed(out, payload, reach)
	}
}

// blockDone accounts for one block's decoded content: against the size the
// frame declared, and into its checksum.
func (d *decoder) blockDone(content []byte) error {
	d.produced += uint64(len(content))
	if d.frame.hasContentSize && d.produced > d.frame.contentSize {
		return errContentSize
	}
	if d.frame.checksum {
		d.hash.write(content)
	}
	return nil
}

// endFrame checks a finished frame's totals. sum is the frame's four checksum
// bytes, when it carries them.
func (d *decoder) endFrame(sum []byte) error {
	if d.frame.hasContentSize && d.produced != d.frame.contentSize {
		return errContentSize
	}
	if d.frame.checksum && uint32(d.hash.sum64()) != binary.LittleEndian.Uint32(sum) {
		return errChecksum
	}
	return nil
}

// decodeCompressed appends a compressed block's content to out. reach is how
// far back from the end of out the block's first match may copy from.
func (d *decoder) decodeCompressed(out, block []byte, reach int) ([]byte, error) {
	lits, n, err := d.decodeLiterals(block)
	if err != nil {
		return out, err
	}
	return d.decodeSequences(out, block[n:], lits, reach)
}

// appendRun appends n copies of b, doubling the copied span each round.
func appendRun(out []byte, b byte, n int) []byte {
	if n == 0 {
		return out
	}
	out = append(out, b)
	return appendMatch(out, 1, n-1)
}
