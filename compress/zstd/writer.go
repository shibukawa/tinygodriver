//go:build tinygo || force_tinygo_logic

package zstd

import (
	"errors"
	"io"
)

const (
	maxBlockSize   = 128 << 10
	matchTableSize = 1 << 12
)

// Writer emits one Zstandard frame. Writer is not safe for concurrent use.
// Close must succeed before Result can be read.
type Writer struct {
	out    *outputWriter
	buf    []byte
	match  [matchTableSize]int32
	closed bool
	err    error
}

// NewWriter starts a streaming frame. The frame has a 128 KiB maximum window
// and does not include an RFC 8878 content checksum; Result provides the
// representation digest used for caching.
func NewWriter(w io.Writer, options ...Option) (*Writer, error) {
	if w == nil {
		return nil, errors.New("zstd: nil writer")
	}
	resolved := resolveOptions(options)
	z := &Writer{
		out: newOutputWriter(w, resolved.etag),
		buf: make([]byte, 0, maxBlockSize),
	}
	// Magic number, frame header descriptor, and a 128 KiB window descriptor.
	if err := z.writeEncoded([]byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x38}); err != nil {
		return nil, err
	}
	return z, nil
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
	written := 0
	for len(p) != 0 {
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
// Flushing before a block fills reduces the compression ratio; Flush is a
// no-op when no input is buffered.
func (z *Writer) Flush() error {
	if z.closed {
		return ErrClosed
	}
	if z.err != nil {
		return z.err
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
	if err := z.writeBlocks(z.buf, true); err != nil {
		z.err = err
		return err
	}
	z.buf = nil
	return nil
}

// Result returns the encoded size and SHA-256 digest after a successful Close.
func (z *Writer) Result() (Result, error) {
	if !z.closed || z.err != nil {
		return Result{}, ErrResultUnavailable
	}
	return z.out.result(), nil
}

func (z *Writer) writeBlocks(p []byte, last bool) error {
	if len(p) < 2 || allSameByte(p) {
		return z.writeBlock(p, last)
	}
	// One run scan serves both the profitability baseline in findMatch and
	// the fallback emission below; it used to be computed twice.
	spans, rleSize := splitRuns(p)
	if match, ok := z.findMatch(p, rleSize); ok {
		return z.writeCompressedBlock(p, match, last)
	}
	for _, s := range spans {
		if err := z.writeBlock(p[s.start:s.end], last && s.end == len(p)); err != nil {
			return err
		}
	}
	return nil
}

// span is one block of the run-split fallback: a qualifying run, or the raw
// stretch between two.
type span struct {
	start, end int
}

// splitRuns cuts p at profitable RLE runs and estimates the encoded size of
// emitting it that way.
func splitRuns(p []byte) (spans []span, size int) {
	rawStart := 0
	for start := 0; start < len(p); {
		end := start + 1
		for end < len(p) && p[end] == p[start] {
			end++
		}
		// A boundary run adds one RLE block; an interior run also splits one
		// raw block into two. These thresholds only split when output shrinks.
		minRun := 8
		if start == 0 || end == len(p) {
			minRun = 5
		}
		if end-start >= minRun {
			if rawStart != start {
				spans = append(spans, span{rawStart, start})
				size += 3 + start - rawStart
			}
			spans = append(spans, span{start, end})
			size += 4
			rawStart = end
		}
		start = end
	}
	if rawStart != len(p) {
		spans = append(spans, span{rawStart, len(p)})
		size += 3 + len(p) - rawStart
	}
	return spans, size
}

type blockMatch struct {
	start  int
	offset int
	length int
}

// matchSkipThreshold is the extension length past which findMatch skips the
// matched span instead of re-extending from every position inside it. Without
// the skip, input shaped like a long repeat that stops short of the block end
// made the scan quadratic: each position inside the repeat re-compared to the
// end of it. Skipping bounds total extension work at the cost of not seeing a
// match that starts inside a span already matched this long.
const matchSkipThreshold = 64

func (z *Writer) findMatch(p []byte, rleSize int) (blockMatch, bool) {
	for i := range z.match {
		z.match[i] = 0
	}
	best := blockMatch{}
	for current := 0; current+4 <= len(p); current++ {
		v := uint32(p[current]) |
			uint32(p[current+1])<<8 |
			uint32(p[current+2])<<16 |
			uint32(p[current+3])<<24
		slot := (v * 2654435761) >> (32 - 12)
		previous := int(z.match[slot]) - 1
		z.match[slot] = int32(current + 1)
		if previous < 0 || p[previous] != p[current] || p[previous+1] != p[current+1] ||
			p[previous+2] != p[current+2] || p[previous+3] != p[current+3] {
			continue
		}
		length := 4
		for current+length < len(p) && p[previous+length] == p[current+length] {
			length++
		}
		if length > best.length {
			best = blockMatch{start: current, offset: current - previous, length: length}
			if current+length == len(p) {
				break
			}
		}
		if length >= matchSkipThreshold {
			current += length - 1
		}
	}
	if best.length < 4 {
		return blockMatch{}, false
	}

	literalCount := len(p) - best.length
	llCode, llBits, _ := literalLengthCode(best.start)
	mlCode, mlBits, _ := matchLengthCode(best.length)
	offsetValue := best.offset + 3 // Explicit offset; avoid repeat-offset codes.
	ofCode := bitLength(uint32(offsetValue)) - 1
	bitstreamSize := int(llBits+mlBits+ofCode+1+7) / 8
	compressedSize := literalHeaderSize(literalCount) + literalCount + 5 + bitstreamSize
	if compressedSize+3 >= rleSize || llCode > 35 || mlCode > 52 || ofCode > 31 {
		return blockMatch{}, false
	}
	return best, true
}

func (z *Writer) writeCompressedBlock(p []byte, match blockMatch, last bool) error {
	literalCount := len(p) - match.length
	llCode, llBits, llExtra := literalLengthCode(match.start)
	mlCode, mlBits, mlExtra := matchLengthCode(match.length)
	offsetValue := uint32(match.offset + 3)
	ofCode := bitLength(offsetValue) - 1
	ofExtra := offsetValue - 1<<ofCode

	var bits uint64
	var bitCount uint8
	appendBits := func(value uint32, count uint8) {
		if count != 0 {
			bits |= uint64(value&((1<<count)-1)) << bitCount
			bitCount += count
		}
	}
	// Sequence bitstreams are decoded backwards. This forward order makes
	// the decoder observe offset, match length, then literal length.
	appendBits(llExtra, llBits)
	appendBits(mlExtra, mlBits)
	appendBits(ofExtra, ofCode)
	appendBits(1, 1) // End marker.
	bitstreamSize := int((bitCount + 7) / 8)

	compressedSize := literalHeaderSize(literalCount) + literalCount + 5 + bitstreamSize
	header := uint32(compressedSize)<<3 | uint32(2)<<1
	if last {
		header |= 1
	}
	if err := z.writeEncoded([]byte{byte(header), byte(header >> 8), byte(header >> 16)}); err != nil {
		return err
	}
	if err := z.writeEncoded(literalHeader(literalCount)); err != nil {
		return err
	}
	if err := z.writeEncoded(p[:match.start]); err != nil {
		return err
	}
	if err := z.writeEncoded(p[match.start+match.length:]); err != nil {
		return err
	}
	// One sequence; RLE mode for literal length, offset, and match length.
	if err := z.writeEncoded([]byte{1, 0x54, llCode, ofCode, mlCode}); err != nil {
		return err
	}
	var stream [8]byte
	for i := range bitstreamSize {
		stream[i] = byte(bits >> (8 * i))
	}
	return z.writeEncoded(stream[:bitstreamSize])
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

func literalHeader(size int) []byte {
	switch literalHeaderSize(size) {
	case 1:
		return []byte{byte(size << 3)}
	case 2:
		v := uint32(size)<<4 | 4
		return []byte{byte(v), byte(v >> 8)}
	default:
		v := uint32(size)<<4 | 12
		return []byte{byte(v), byte(v >> 8), byte(v >> 16)}
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

func literalLengthCode(length int) (code, bits uint8, extra uint32) {
	return lengthCode(length, literalLengthBases[:], literalLengthBits[:])
}

func matchLengthCode(length int) (code, bits uint8, extra uint32) {
	return lengthCode(length, matchLengthBases[:], matchLengthBits[:])
}

func lengthCode(length int, bases []int, widths []uint8) (code, bits uint8, extra uint32) {
	for i := len(bases) - 1; i >= 0; i-- {
		if length >= bases[i] {
			return uint8(i), widths[i], uint32(length - bases[i])
		}
	}
	return 0, 0, 0
}

func bitLength(v uint32) uint8 {
	var n uint8
	for v != 0 {
		n++
		v >>= 1
	}
	return n
}

func (z *Writer) writeBlock(p []byte, last bool) error {
	typ := uint32(0) // raw block
	payload := p
	if len(p) > 1 && allSameByte(p) {
		typ = 1 // RLE block
		payload = p[:1]
	}
	header := uint32(len(p))<<3 | typ<<1
	if last {
		header |= 1
	}
	if err := z.writeEncoded([]byte{byte(header), byte(header >> 8), byte(header >> 16)}); err != nil {
		return err
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
