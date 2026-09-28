//go:build tinygo || force_tinygo_logic

// Decoding, for TinyGo: frames, blocks, and the state a frame's blocks share.
//
// The encoder in this package writes a small subset of RFC 8878, but a decoder
// takes frames from anywhere, so this one implements the whole format apart
// from dictionaries: every literals and sequence table mode, the repeat
// offsets across blocks, windows up to a configured limit, skippable frames,
// and content checksums. Every length and table on the wire is checked before
// it is used, since the input is not trusted.

package zstd

import (
	"encoding/binary"
	"errors"
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

	// maxFramePrealloc bounds how much of a declared Frame_Content_Size is
	// allocated before any of it is decoded. The field is the sender's claim,
	// and a few bytes of header must not buy megabytes of memory.
	maxFramePrealloc = 1 << 20
)

var (
	errBadMagic       = errors.New("zstd: not a Zstandard frame")
	errDictionary     = errors.New("zstd: frame requires a dictionary")
	errWindowTooLarge = errors.New("zstd: frame window exceeds the decoder's limit")
	errChecksum       = errors.New("zstd: content checksum mismatch")
)

// corruption reports input that violates the format, naming where the decoder
// found it. It is a string type so that each value is a constant.
type corruption string

func (c corruption) Error() string { return "zstd: corrupt input: " + string(c) }

const (
	errCorruptFrame     corruption = "frame header"
	errCorruptBlock     corruption = "block header"
	errCorruptLiterals  corruption = "literals section"
	errCorruptSequences corruption = "sequences section"
	errCorruptTable     corruption = "FSE table description"
	errCorruptBitstream corruption = "entropy-coded bitstream"
	errCorruptOffset    corruption = "match offset"
	errContentSize      corruption = "content size differs from the frame header"
)

// frameParams is what a frame header declares.
type frameParams struct {
	windowSize     uint64
	contentSize    uint64
	hasContentSize bool
	checksum       bool
	dictID         uint32
}

// parseFrameParams parses the frame header that follows the magic number,
// returning it and the bytes it took.
func parseFrameParams(src []byte) (frameParams, int, error) {
	var h frameParams
	if len(src) == 0 {
		return h, 0, io.ErrUnexpectedEOF
	}
	fhd := src[0]
	if fhd&8 != 0 {
		return h, 0, errCorruptFrame // reserved bit
	}
	single := fhd&0x20 != 0
	h.checksum = fhd&4 != 0
	dictSize := [4]int{0, 1, 2, 4}[fhd&3]
	contentSize := [4]int{0, 2, 4, 8}[fhd>>6]
	if fhd>>6 == 0 && single {
		contentSize = 1
	}
	n := 1 + dictSize + contentSize
	if !single {
		n++
	}
	if len(src) < n {
		return h, 0, io.ErrUnexpectedEOF
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

	// This frame's window and the largest block content it allows.
	window   int
	blockMax int

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

// maxWindowCeiling is the largest window any decoder accepts, whatever its
// configured limit: the reference decoder's ceiling on 32-bit platforms, which
// keeps every window size an int on TinyGo's wasm targets.
const maxWindowCeiling = 1 << 30

func newDecoder(maxWindow uint64) *decoder {
	return &decoder{maxWindow: min(maxWindow, maxWindowCeiling)}
}

// decodeAll appends the content of every frame in src to dst. Skippable frames
// are skipped; anything else that is not a whole frame is an error.
func (d *decoder) decodeAll(dst, src []byte) ([]byte, error) {
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
		if dst, src, err = d.decodeFrame(dst, src[4:]); err != nil {
			return dst, err
		}
	}
	return dst, nil
}

// decodeFrame decodes the frame at the front of src, which starts after the
// magic number, and returns what follows it.
func (d *decoder) decodeFrame(dst, src []byte) ([]byte, []byte, error) {
	h, n, err := parseFrameParams(src)
	if err != nil {
		return dst, src, err
	}
	src = src[n:]
	if h.dictID != 0 {
		return dst, src, errDictionary
	}
	if h.windowSize > d.maxWindow {
		return dst, src, errWindowTooLarge
	}
	d.resetFrame(int(h.windowSize))
	if h.checksum {
		d.hash.reset()
	}

	start := len(dst)
	if h.hasContentSize {
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
		bh := uint32(src[0]) | uint32(src[1])<<8 | uint32(src[2])<<16
		src = src[3:]
		last = bh&1 != 0
		size := int(bh >> 3)
		// The size is the block's content for raw and RLE blocks and its
		// encoded form for compressed ones; the format caps both the same.
		if size > d.blockMax {
			return dst, src, errCorruptBlock
		}
		blockStart := len(dst)
		switch bh >> 1 & 3 {
		case blockRaw:
			if len(src) < size {
				return dst, src, io.ErrUnexpectedEOF
			}
			dst = append(dst, src[:size]...)
			src = src[size:]
		case blockRLE:
			if len(src) < 1 {
				return dst, src, io.ErrUnexpectedEOF
			}
			dst = appendRun(dst, src[0], size)
			src = src[1:]
		case blockCompressed:
			if len(src) < size {
				return dst, src, io.ErrUnexpectedEOF
			}
			reach := min(len(dst)-start, d.window)
			if dst, err = d.decodeCompressed(dst, src[:size], reach); err != nil {
				return dst, src, err
			}
			src = src[size:]
		default:
			return dst, src, errCorruptBlock // reserved type
		}
		if h.hasContentSize && uint64(len(dst)-start) > h.contentSize {
			return dst, src, errContentSize
		}
		if h.checksum {
			d.hash.write(dst[blockStart:])
		}
	}

	if h.hasContentSize && uint64(len(dst)-start) != h.contentSize {
		return dst, src, errContentSize
	}
	if h.checksum {
		if len(src) < 4 {
			return dst, src, io.ErrUnexpectedEOF
		}
		if uint32(d.hash.sum64()) != binary.LittleEndian.Uint32(src) {
			return dst, src, errChecksum
		}
		src = src[4:]
	}
	return dst, src, nil
}

// resetFrame starts the per-frame state for a frame with the given window,
// which the caller has already checked against the decoder's limit.
func (d *decoder) resetFrame(window int) {
	d.window = window
	d.blockMax = min(window, maxBlockSize)
	d.rep = [3]uint32{1, 4, 8}
	d.huffValid = false
	d.seqValid = false
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
