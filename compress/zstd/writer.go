//go:build tinygo || force_tinygo_logic

package zstd

import (
	"encoding/binary"
	"io"
	"math/bits"
)

const (
	maxBlockSize = 128 << 10

	// matchTableBits sizes the match-finder hash table. One candidate is kept per
	// slot, so this trades memory for how often a repeat is actually found.
	matchTableBits = 12
	matchTableSize = 1 << matchTableBits
)

// Writer emits one Zstandard frame. Writer is not safe for concurrent use.
// Close must succeed before Result can be read.
type Writer struct {
	out   *outputWriter
	buf   []byte
	match [matchTableSize]int32

	// Slots the last scan wrote in the match table, so a small block's scan only
	// clears what it dirtied; matchAllDirty stands in for the list when a scan
	// wrote more slots than tracking them is worth.
	matchTouched  []uint16
	matchAllDirty bool

	// Reused across blocks so that a frame allocates these once.
	seqs     []sequence
	seqCodes []seqCode
	literals []byte
	block    []byte
	spans    []span

	// rep1 is repeat-offset slot one as the decoder holds it entering the next
	// compressed block: 1 at frame start, then the last offset of each
	// compressed block that reached the wire. The decoder carries this history
	// across the blocks of a frame, so the encoder must too, or an
	// Offset_Value 1 early in a later block names the wrong distance and the
	// frame decodes to the wrong bytes at the right length.
	rep1 uint32

	// Symbol histograms and the distributions fitted to them, per block.
	llCounts [36]uint32
	ofCounts [32]uint32
	mlCounts [53]uint32
	llNorm   [36]int16
	ofNorm   [32]int16
	mlNorm   [53]int16

	// Backing store for the per-block fitted FSE tables, one per symbol stream.
	llCT ctableScratch
	ofCT ctableScratch
	mlCT ctableScratch

	// Literal histogram, and the coded literals section built from it.
	// litCountsReady says the histogram was already taken while the literals
	// were gathered, so the literal coder need not take it again.
	litCounts      [256]uint32
	litCountsReady bool
	litBody        []byte

	// The literal code fitted per block, and the tree builder's working set.
	huffTab huffTable
	huffSc  huffScratch

	// Block headers are three bytes, four when an RLE payload rides along; a
	// fixed field keeps them off the heap, since what is handed to an io.Writer
	// always escapes.
	hdr [4]byte

	wroteHeader bool
	closed      bool
	err         error
}

// NewWriter starts a streaming frame. Constructing a Writer writes nothing to
// the destination: as in compress/gzip, the frame header goes out with the
// first Write, Flush, or Close, so an encoder built and then abandoned leaves
// the destination untouched. The frame has a 128 KiB maximum window and does
// not include an RFC 8878 content checksum; Result provides the representation
// digest used for caching.
func NewWriter(w io.Writer, options ...Option) (*Writer, error) {
	if w == nil {
		return nil, errNilWriter
	}
	resolved := resolveOptions(options)
	return &Writer{
		out:  newOutputWriter(w, resolved.etag),
		buf:  make([]byte, 0, maxBlockSize),
		rep1: 1,
	}, nil
}

// frameHeader is the same for every frame this encoder writes: magic number,
// frame header descriptor, and a 128 KiB window descriptor.
var frameHeader = []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x38}

// writeFrameHeader emits the frame header the first time the caller asks for
// output. A server that builds an encoder before rendering can then still
// answer with an uncompressed error response, because nothing has reached the
// ResponseWriter and the status is not yet committed.
func (z *Writer) writeFrameHeader() error {
	if z.wroteHeader {
		return nil
	}
	z.wroteHeader = true
	return z.writeEncoded(frameHeader)
}

// Write adds uncompressed input to the frame. At most one block (128 KiB) is
// retained for match finding and profitable Zstandard RLE splitting.
func (z *Writer) Write(p []byte) (int, error) {
	if z.closed {
		return 0, ErrClosed
	}
	if z.err != nil {
		return 0, z.err
	}
	if err := z.writeFrameHeader(); err != nil {
		z.err = err
		return 0, err
	}
	written := 0
	for len(p) != 0 {
		// A full block with nothing buffered ahead of it can be compressed where
		// it lies; staging it through z.buf would only be a copy.
		if len(z.buf) == 0 && len(p) >= maxBlockSize {
			if err := z.writeBlocks(p[:maxBlockSize], false); err != nil {
				z.err = err
				return written, err
			}
			p = p[maxBlockSize:]
			written += maxBlockSize
			continue
		}
		n := maxBlockSize - len(z.buf)
		if n > len(p) {
			n = len(p)
		}
		z.buf = append(z.buf, p[:n]...)
		p = p[n:]
		written += n
		if len(z.buf) == maxBlockSize {
			if err := z.writeBlocks(z.buf, false); err != nil {
				z.err = err
				return written, err
			}
			z.buf = z.buf[:0]
		}
	}
	return written, nil
}

// Flush emits the buffered input as complete blocks so that everything written
// so far can be decoded, and returns once those bytes reach the destination.
// It does not end the frame and it does not flush the destination itself.
// Flushing before a block fills reduces the compression ratio; Flush writes
// nothing beyond a still pending frame header when no input is buffered.
func (z *Writer) Flush() error {
	if z.closed {
		return ErrClosed
	}
	if z.err != nil {
		return z.err
	}
	if err := z.writeFrameHeader(); err != nil {
		z.err = err
		return err
	}
	if len(z.buf) == 0 {
		return nil
	}
	if err := z.writeBlocks(z.buf, false); err != nil {
		z.err = err
		return err
	}
	z.buf = z.buf[:0]
	return nil
}

// Close finishes the frame. It does not close the destination.
func (z *Writer) Close() error {
	if z.closed {
		return z.err
	}
	z.closed = true
	if z.err != nil {
		return z.err
	}
	if err := z.writeFrameHeader(); err != nil {
		z.err = err
		return err
	}
	if err := z.writeBlocks(z.buf, true); err != nil {
		z.err = err
		return err
	}
	// Truncated rather than released: a Writer that is dropped after Close is
	// collected whole anyway, and one that is kept is being pooled, where
	// handing the block buffer back to the allocator is the opposite of the
	// point. Reset reclaims it.
	z.buf = z.buf[:0]
	return nil
}

// Result returns the encoded size and SHA-256 digest after a successful Close.
func (z *Writer) Result() (Result, error) {
	if !z.closed || z.err != nil {
		return Result{}, ErrResultUnavailable
	}
	return z.out.result(), nil
}

// Reset starts a new frame writing to w, discarding any input the previous
// frame had buffered but keeping the block buffer, the match table, and the
// per-block scratch the encoder has grown into. That is what makes an encoder
// worth pooling: the block buffer and match table are the bulk of its
// footprint, and the scratch is what spares a pooled encoder the growth
// allocations a fresh one pays on its first blocks.
//
// The ETag setting chosen at NewWriter is retained. As with NewWriter, nothing
// reaches w until the caller writes, flushes, or closes. Passing a nil w leaves
// the Writer in the error state that NewWriter would have reported.
func (z *Writer) Reset(w io.Writer) {
	if cap(z.buf) == 0 {
		z.buf = make([]byte, 0, maxBlockSize)
	}
	z.buf = z.buf[:0]
	z.wroteHeader = false
	z.closed = false
	z.rep1 = 1
	if w == nil {
		z.err = errNilWriter
		return
	}
	z.err = nil
	z.out.reset(w)
}

func (z *Writer) writeBlocks(p []byte, last bool) error {
	if len(p) == 0 {
		return z.writeBlock(p, last)
	}
	for len(p) > 0 {
		n := z.findSequences(p)
		chunk := p[:n]
		isLast := last && n == len(p)
		if err := z.writeOneBlock(chunk, isLast); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

// writeOneBlock emits chunk, whose sequences are already in z.seqs, as whichever
// of a compressed block or the stored fallback comes out smaller.
func (z *Writer) writeOneBlock(p []byte, last bool) error {
	if len(p) < 2 {
		return z.writeBlock(p, last)
	}

	// One run scan serves the whole-block RLE test, the profitability baseline a
	// compressed block is measured against, and the fallback emission below.
	var rleSize int
	var allSame bool
	z.spans, rleSize, allSame = splitRuns(p, z.spans[:0])
	if allSame {
		return z.emitBlock(1, len(p), p[:1], last)
	}

	if cap(z.block) < len(p) {
		z.block = make([]byte, 0, len(p)+64)
	}
	// The grown buffer is kept either way; only the ok answer decides whether
	// its contents are written or the fallback runs.
	block, ok := z.appendCompressedBlock(z.block[:0], p, last, rleSize)
	z.block = block
	if ok {
		return z.writeEncoded(z.block)
	}
	for _, s := range z.spans {
		isLast := last && s.end == len(p)
		if s.run {
			// A qualifying run is at least two bytes, so it is always worth the
			// RLE form; deciding that here spares the payload another scan.
			if err := z.emitBlock(1, s.end-s.start, p[s.start:s.start+1], isLast); err != nil {
				return err
			}
			continue
		}
		if err := z.writeBlock(p[s.start:s.end], isLast); err != nil {
			return err
		}
	}
	return nil
}

// span is one block of the run-split fallback: a qualifying run, or the raw
// stretch between two.
type span struct {
	start, end int
	run        bool
}

// splitRuns cuts p at profitable RLE runs, appending the pieces to spans, and
// estimates the encoded size of emitting it that way. It also reports whether p
// is one run from end to end, however short, which is the whole-block RLE case
// the caller handles without any of this. len(p) must be at least 1.
func splitRuns(p []byte, spans []span) (out []span, size int, allSame bool) {
	rawStart := 0
	for start := 0; start < len(p); {
		// A qualifying run needs adjacent equal bytes, and a byte differing from
		// its successor never starts one. Comparing a word against itself one
		// byte over finds any adjacent pair in eight positions at once, which is
		// what lets content with no runs -- most content -- move at eight bytes a
		// step instead of one. The skipped positions are all runs of length one,
		// which the per-run loop below would have stepped over singly.
		for start+9 <= len(p) {
			x := binary.LittleEndian.Uint64(p[start:]) ^ binary.LittleEndian.Uint64(p[start+1:])
			if (x-0x0101010101010101)&^x&0x8080808080808080 != 0 {
				break
			}
			start += 8
		}
		end := start + 1
		for end < len(p) && p[end] == p[start] {
			end++
		}
		if start == 0 && end == len(p) {
			return spans, 4, true
		}
		// A boundary run adds one RLE block; an interior run also splits one
		// raw block into two. These thresholds only split when output shrinks.
		minRun := 8
		if start == 0 || end == len(p) {
			minRun = 5
		}
		if end-start >= minRun {
			if rawStart != start {
				spans = append(spans, span{rawStart, start, false})
				size += 3 + start - rawStart
			}
			spans = append(spans, span{start, end, true})
			size += 4
			rawStart = end
		}
		start = end
	}
	if rawStart != len(p) {
		spans = append(spans, span{rawStart, len(p), false})
		size += 3 + len(p) - rawStart
	}
	return spans, size, false
}

func literalHeaderSize(size int) int {
	switch {
	case size <= 31:
		return 1
	case size <= 4095:
		return 2
	default:
		return 3
	}
}

// appendLiteralHeader states a literals section's regenerated size for the block
// types that carry the bytes verbatim: raw, and RLE where one byte stands for
// the run.
func appendLiteralHeader(dst []byte, size, blockType int) []byte {
	switch literalHeaderSize(size) {
	case 1:
		return append(dst, byte(size<<3|blockType))
	case 2:
		v := uint32(size)<<4 | 1<<2 | uint32(blockType)
		return append(dst, byte(v), byte(v>>8))
	default:
		v := uint32(size)<<4 | 3<<2 | uint32(blockType)
		return append(dst, byte(v), byte(v>>8), byte(v>>16))
	}
}

var literalLengthBases = [...]int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
	16, 18, 20, 22, 24, 28, 32, 40, 48, 64, 128, 256, 512,
	1024, 2048, 4096, 8192, 16384, 32768, 65536,
}

var literalLengthBits = [...]uint8{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	1, 1, 1, 1, 2, 2, 3, 3, 4, 6, 7, 8, 9, 10, 11, 12,
	13, 14, 15, 16,
}

var matchLengthBases = [...]int{
	3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
	17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30,
	31, 32, 33, 34, 35, 37, 39, 41, 43, 47, 51, 59, 67, 83,
	99, 131, 259, 515, 1027, 2051, 4099, 8195, 16387, 32771, 65539,
}

var matchLengthBits = [...]uint8{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	1, 1, 1, 1, 2, 2, 3, 3, 4, 4, 5, 7, 8, 9, 10, 11,
	12, 13, 14, 15, 16,
}

// The code for a length is a table lookup for the dense low range, where the
// bases step irregularly, and a bit-length formula above it, where each code
// spans exactly one power of two. Both halves are derived at startup from the
// bases by the same top-down scan they replace, so they cannot disagree with
// it; the seams were checked against that scan across the whole length range.
var (
	llCodeSmall [64]uint8
	mlCodeSmall [128]uint8
)

func init() {
	for l := range llCodeSmall {
		llCodeSmall[l] = lengthCode(l, literalLengthBases[:])
	}
	for l := range mlCodeSmall {
		mlCodeSmall[l] = lengthCode(l+3, matchLengthBases[:])
	}
}

func literalLengthCode(length int) uint8 {
	if length < 64 {
		return llCodeSmall[length]
	}
	c := uint8(18 + bits.Len32(uint32(length)))
	if c > 35 {
		c = 35
	}
	return c
}

func matchLengthCode(length int) uint8 {
	if uint(length-3) < 128 {
		return mlCodeSmall[length-3]
	}
	c := uint8(35 + bits.Len32(uint32(length-3)))
	if c > 52 {
		c = 52
	}
	return c
}

// lengthCode is the reference mapping the startup tables are built from.
func lengthCode(length int, bases []int) uint8 {
	for i := len(bases) - 1; i >= 0; i-- {
		if length >= bases[i] {
			return uint8(i)
		}
	}
	return 0
}

func (z *Writer) writeBlock(p []byte, last bool) error {
	typ := 0
	payload := p
	if len(p) > 1 && allSameByte(p) {
		typ = 1 // RLE block
		payload = p[:1]
	}
	return z.emitBlock(typ, len(p), payload, last)
}

// emitBlock writes one raw or RLE block: the three-byte header stating size and
// type, then the payload. An RLE payload is a single byte and travels in the
// same Write as its header, which also keeps it to one digest update.
func (z *Writer) emitBlock(typ, size int, payload []byte, last bool) error {
	header := uint32(size)<<3 | uint32(typ)<<1
	if last {
		header |= 1
	}
	z.hdr[0] = byte(header)
	z.hdr[1] = byte(header >> 8)
	z.hdr[2] = byte(header >> 16)
	if len(payload) == 1 {
		z.hdr[3] = payload[0]
		return z.writeEncoded(z.hdr[:4])
	}
	if err := z.writeEncoded(z.hdr[:3]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	return z.writeEncoded(payload)
}

func allSameByte(p []byte) bool {
	first := p[0]
	for _, b := range p[1:] {
		if b != first {
			return false
		}
	}
	return true
}

func (z *Writer) writeEncoded(p []byte) error {
	_, err := z.out.Write(p)
	return err
}
