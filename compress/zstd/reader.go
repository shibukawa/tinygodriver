//go:build tinygo || force_tinygo_logic

// The decoding API for TinyGo: DecodeAll over a byte slice, and Reader over a
// stream. Both drive the frame steps in decoder.go, so they accept and reject
// exactly the same input.

package zstd

import (
	"bufio"
	"encoding/binary"
	"io"
	"sync"
)

// readBufferSize is the Reader's input buffer. Block payloads larger than it
// are read straight into the block buffer; it is there so that the small
// reads -- magic numbers, three-byte block headers -- do not each reach the
// underlying reader.
const readBufferSize = 4 << 10

var decoderPool sync.Pool

// DecodeAll decodes every frame in src and appends the content to dst.
// Skippable frames are skipped, and empty input decodes to nothing. On error,
// what the returned slice holds beyond dst is unspecified.
func DecodeAll(dst, src []byte, options ...DecoderOption) ([]byte, error) {
	resolved, err := resolveDecoderOptions(options)
	if err != nil {
		return dst, err
	}
	d, _ := decoderPool.Get().(*decoder)
	if d == nil {
		d = &decoder{}
	}
	d.configure(resolved)
	dst, err = d.decodeAll(dst, src)
	decoderPool.Put(d)
	return dst, err
}

// Reader decompresses a stream of Zstandard frames, skipping skippable ones.
// Reader is not safe for concurrent use.
//
// It decodes a block at a time, as Read asks for content, and holds at most
// twice the window the current frame declares plus one block: the window is
// what later matches may copy from. WithMaxWindow therefore bounds its memory.
type Reader struct {
	d     decoder
	in    *bufio.Reader
	state readState
	hdr   [maxFrameHeader]byte

	// hist is decoded content: the window behind the read position, then
	// what the caller has yet to read, from pos on.
	hist []byte
	pos  int

	block  []byte // the payload of the block being decoded
	total  int64  // content returned, counted against the output limit
	err    error  // sticky, once the stream has failed or ended
	closed bool
}

type readState uint8

const (
	readFrame    readState = iota // before a frame, where the stream may end
	readBlock                     // inside a frame
	readFrameEnd                  // after a frame's last block
)

// NewReader returns a Reader that decompresses r. Unlike compress/gzip's
// NewReader it reads nothing until the first Read, so it cannot fail on the
// stream's content; it fails only on an invalid option. r may be nil, for a
// Reader that a pool will Reset before use; reading it first fails.
func NewReader(r io.Reader, options ...DecoderOption) (*Reader, error) {
	resolved, err := resolveDecoderOptions(options)
	if err != nil {
		return nil, err
	}
	z := &Reader{}
	z.d.configure(resolved)
	z.Reset(r)
	return z, nil
}

// Reset discards the Reader's state and makes it decompress r, keeping its
// options and buffers, so that a pool of Readers allocates once. It makes a
// closed Reader usable again, and like NewReader it reads nothing.
func (z *Reader) Reset(r io.Reader) error {
	if r == nil {
		z.err = errNilReader
		return errNilReader
	}
	if z.in == nil {
		z.in = bufio.NewReaderSize(r, readBufferSize)
	} else {
		z.in.Reset(r)
	}
	z.state = readFrame
	z.hist = z.hist[:0]
	z.pos = 0
	z.total = 0
	z.err = nil
	z.closed = false
	return nil
}

// Close releases the Reader's buffers. It does not close the underlying
// reader. Read reports ErrClosed until Reset.
func (z *Reader) Close() error {
	z.closed = true
	z.in = nil
	z.hist = nil
	z.block = nil
	z.d.lits = nil
	return nil
}

// Read decompresses into p. It returns io.EOF at the end of the last frame,
// io.ErrUnexpectedEOF if the stream ends inside one, and ErrOutputTooLarge
// once it has returned as much content as WithMaxOutput allows and more
// remains.
func (z *Reader) Read(p []byte) (int, error) {
	if z.closed {
		return 0, ErrClosed
	}
	for z.pos == len(z.hist) {
		if z.err != nil {
			return 0, z.err
		}
		if len(p) == 0 {
			return 0, nil
		}
		z.err = z.next()
	}
	avail := z.hist[z.pos:]
	if limit := z.d.maxOutput; limit > 0 {
		left := limit - z.total
		if left <= 0 {
			return 0, ErrOutputTooLarge
		}
		if int64(len(avail)) > left {
			avail = avail[:left]
		}
	}
	n := copy(p, avail)
	z.pos += n
	z.total += int64(n)
	return n, nil
}

// next advances the stream by one step -- a frame header, a block, or a
// frame's end -- leaving whatever content it decodes unread in hist. It
// returns io.EOF where the stream may end: before a frame, with nothing read.
func (z *Reader) next() error {
	switch z.state {
	case readFrame:
		return z.readFrameHeader()
	case readBlock:
		return z.readBlock()
	default:
		return z.readFrameEnd()
	}
}

func (z *Reader) readFrameHeader() error {
	if _, err := io.ReadFull(z.in, z.hdr[:4]); err != nil {
		return err // io.EOF only if nothing at all was read
	}
	magic := binary.LittleEndian.Uint32(z.hdr[:])
	if magic&skippableMagicMask == skippableMagic {
		if _, err := io.ReadFull(z.in, z.hdr[:4]); err != nil {
			return unexpected(err)
		}
		return z.skip(binary.LittleEndian.Uint32(z.hdr[:]))
	}
	if magic != frameMagic {
		return errBadMagic
	}
	if _, err := io.ReadFull(z.in, z.hdr[:1]); err != nil {
		return unexpected(err)
	}
	n := frameHeaderSize(z.hdr[0])
	if _, err := io.ReadFull(z.in, z.hdr[1:n]); err != nil {
		return unexpected(err)
	}
	h, _, err := parseFrameParams(z.hdr[:n])
	if err != nil {
		return err
	}
	if err := z.d.beginFrame(h); err != nil {
		return err
	}
	// A frame's matches never reach into an earlier frame, and everything
	// before this point has been read, so the history starts over.
	z.hist = z.hist[:0]
	z.pos = 0
	z.state = readBlock
	return nil
}

// skip discards a skippable frame's payload, in pieces so that a size near
// 4 GiB never has to fit a 32-bit int.
func (z *Reader) skip(n uint32) error {
	for n > 0 {
		m, err := z.in.Discard(int(min(n, 1<<20)))
		n -= uint32(m)
		if err != nil {
			return unexpected(err)
		}
	}
	return nil
}

func (z *Reader) readBlock() error {
	if _, err := io.ReadFull(z.in, z.hdr[:3]); err != nil {
		return unexpected(err)
	}
	typ, size, last, err := z.d.parseBlockHeader(z.hdr[:3])
	if err != nil {
		return err
	}
	payload := blockPayloadSize(typ, size)
	if cap(z.block) < payload {
		// Sized for the frame's largest block, and never less than the one
		// byte of an RLE block, which a frame of empty content still allows.
		z.block = make([]byte, max(payload, z.d.blockMax))
	}
	block := z.block[:payload]
	if _, err := io.ReadFull(z.in, block); err != nil {
		return unexpected(err)
	}

	z.makeRoom()
	start := len(z.hist)
	z.hist, err = z.d.appendBlock(z.hist, typ, size, block)
	if err == nil {
		err = z.d.blockDone(z.hist[start:])
	}
	if err != nil {
		// Content from a block that failed is not handed out.
		z.hist = z.hist[:start]
		return err
	}
	if last {
		z.state = readFrameEnd
	}
	return nil
}

func (z *Reader) readFrameEnd() error {
	var sum []byte
	if z.d.frame.checksum {
		if _, err := io.ReadFull(z.in, z.hdr[:4]); err != nil {
			return unexpected(err)
		}
		sum = z.hdr[:4]
	}
	if err := z.d.endFrame(sum); err != nil {
		return err
	}
	z.state = readFrame
	return nil
}

// makeRoom ensures hist can take a whole block without reallocating, keeping
// the last window of content, which is what the block's matches copy from.
// Everything before the block has been read by now.
//
// The buffer grows to twice the window plus a block, and from then on slides
// the last window to the front. A slide frees at least a window's worth of
// space, so it happens at most once per window of content, and each byte is
// copied at most once more than decoding already copies it.
func (z *Reader) makeRoom() {
	need := z.d.blockMax
	if cap(z.hist)-len(z.hist) >= need {
		return
	}
	limit := 2*z.d.window + need
	if cap(z.hist) < limit {
		grown := make([]byte, len(z.hist), min(limit, max(2*cap(z.hist), len(z.hist)+need, 64<<10)))
		copy(grown, z.hist)
		z.hist = grown
		if cap(z.hist)-len(z.hist) >= need {
			return
		}
	}
	keep := min(len(z.hist), z.d.window)
	copy(z.hist, z.hist[len(z.hist)-keep:])
	z.hist = z.hist[:keep]
	z.pos = keep
}

// unexpected reports the end of the stream inside a frame for what it is.
func unexpected(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}
