//go:build !tinygo && !force_tinygo_logic

package zstd

import (
	"bytes"
	"errors"
	"io"
	"sync"

	kzstd "github.com/klauspost/compress/zstd"
)

// Decoding on host Go, through github.com/klauspost/compress/zstd, behind the
// same API and errors as the TinyGo decoder.
//
// klauspost decodes synchronously here, one block at a time, and the output
// limit is counted in this file rather than handed to klauspost: its own limit
// applies only to its in-memory DecodeAll, which would have to finish before
// the limit could be checked.

// hostDecoder is what DecodeAll reuses: a klauspost decoder and the three
// small objects every call would otherwise allocate around it.
type hostDecoder struct {
	dec *kzstd.Decoder
	src bytes.Reader
	in  source
	out appendWriter
}

// Pools of hostDecoders, one per window limit, since that is the one option
// baked into a klauspost decoder. The default has a pool of its own so that
// the common call needs no map lookup, which would box its key.
var (
	defaultPool sync.Pool
	windowPools sync.Map // int -> *sync.Pool
)

func poolFor(maxWindow int) *sync.Pool {
	if maxWindow == defaultMaxWindow {
		return &defaultPool
	}
	if v, ok := windowPools.Load(maxWindow); ok {
		return v.(*sync.Pool)
	}
	v, _ := windowPools.LoadOrStore(maxWindow, &sync.Pool{})
	return v.(*sync.Pool)
}

func newKlauspostDecoder(maxWindow int) (*kzstd.Decoder, error) {
	return kzstd.NewReader(nil,
		kzstd.WithDecoderConcurrency(1),
		kzstd.WithDecoderLowmem(true),
		kzstd.WithDecoderMaxWindow(uint64(maxWindow)),
	)
}

// DecodeAll decodes every frame in src and appends the content to dst.
// Skippable frames are skipped, and empty input decodes to nothing. On error,
// what the returned slice holds beyond dst is unspecified.
func DecodeAll(dst, src []byte, options ...DecoderOption) ([]byte, error) {
	resolved, err := resolveDecoderOptions(options)
	if err != nil {
		return dst, err
	}
	pool := poolFor(resolved.maxWindow)
	h, _ := pool.Get().(*hostDecoder)
	if h == nil {
		h = &hostDecoder{}
		if h.dec, err = newKlauspostDecoder(resolved.maxWindow); err != nil {
			return dst, err
		}
	}
	h.src.Reset(src)
	h.in = source{r: &h.src}
	h.out = appendWriter{b: dst, left: resolved.maxOutput, limited: resolved.maxOutput > 0}
	if err = h.dec.Reset(&h.in); err == nil || err == io.EOF {
		_, err = h.dec.WriteTo(&h.out)
	}
	dst, err = h.out.b, mapDecodeError(err, &h.in)
	// Drop the references to the caller's slices before pooling.
	h.src.Reset(nil)
	h.out.b = nil
	pool.Put(h)
	return dst, err
}

// appendWriter collects DecodeAll's output, failing the write that would take
// it past the output limit.
type appendWriter struct {
	b       []byte
	left    int64
	limited bool
}

func (w *appendWriter) Write(p []byte) (int, error) {
	if w.limited && int64(len(p)) > w.left {
		return 0, ErrOutputTooLarge
	}
	w.b = append(w.b, p...)
	w.left -= int64(len(p))
	return len(p), nil
}

// Reader decompresses a stream of Zstandard frames, skipping skippable ones.
// Reader is not safe for concurrent use.
type Reader struct {
	dec     *kzstd.Decoder // created at the first Read, kept across Reset
	in      source         // the stream, wrapped
	pending bool           // in has not been handed to dec yet
	options decoderOptions
	total   int64
	err     error
	closed  bool
}

// NewReader returns a Reader that decompresses r. Unlike compress/gzip's
// NewReader it reads nothing until the first Read, so it cannot fail on the
// stream's content; it fails only on an invalid option. r may be nil, for a
// Reader that a pool will Reset before use; reading it first fails.
func NewReader(r io.Reader, options ...DecoderOption) (*Reader, error) {
	resolved, err := resolveDecoderOptions(options)
	if err != nil {
		return nil, err
	}
	z := &Reader{options: resolved}
	z.Reset(r)
	return z, nil
}

// Reset discards the Reader's state and makes it decompress r, keeping its
// options and decoder, so that a pool of Readers allocates once. It makes a
// closed Reader usable again, and like NewReader it reads nothing.
func (z *Reader) Reset(r io.Reader) error {
	if r == nil {
		z.err = errNilReader
		return errNilReader
	}
	z.in = source{r: r}
	z.pending = true
	z.total = 0
	z.err = nil
	z.closed = false
	return nil
}

// Close releases the Reader's decoder. It does not close the underlying
// reader. Read reports ErrClosed until Reset.
func (z *Reader) Close() error {
	if z.dec != nil {
		z.dec.Close()
		z.dec = nil
	}
	z.closed = true
	z.in = source{}
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
	if z.err != nil {
		return 0, z.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if z.pending {
		// klauspost reads the first frame header at Reset, so the stream is
		// handed over here rather than in Reset, which keeps construction
		// free of reads as it is under TinyGo.
		if z.dec == nil {
			dec, err := newKlauspostDecoder(z.options.maxWindow)
			if err != nil {
				z.err = err
				return 0, err
			}
			z.dec = dec
		}
		z.pending = false
		if err := z.dec.Reset(&z.in); err != nil {
			z.err = mapDecodeError(err, &z.in)
			return 0, z.err
		}
	}
	if limit := z.options.maxOutput; limit > 0 {
		left := limit - z.total
		if left <= 0 {
			// At the limit, it is an error only if content remains, which
			// takes a byte to find out.
			var probe [1]byte
			n, err := z.dec.Read(probe[:])
			if n > 0 {
				z.err = ErrOutputTooLarge
			} else {
				z.err = mapDecodeError(err, &z.in)
			}
			return 0, z.err
		}
		if int64(len(p)) > left {
			p = p[:left]
		}
	}
	n, err := z.dec.Read(p)
	z.total += int64(n)
	if err != nil {
		z.err = mapDecodeError(err, &z.in)
	}
	return n, z.err
}

// source hands a stream to klauspost as a plain io.Reader. That matters
// twice: klauspost decodes a *bytes.Buffer, or anything with its Bytes and
// Len, whole and in memory at Reset, out of the output limit's reach; and
// source remembers the stream's own read errors, so they are reported as they
// are rather than as corrupt input.
type source struct {
	r   io.Reader
	err error
}

func (s *source) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF {
		s.err = err
	}
	return n, err
}

// hostCorruption marks a klauspost error as ErrCorrupt, keeping it for
// errors.As and the message.
type hostCorruption struct{ err error }

func (e hostCorruption) Error() string        { return "zstd: corrupt input: " + e.err.Error() }
func (e hostCorruption) Is(target error) bool { return target == ErrCorrupt }
func (e hostCorruption) Unwrap() error        { return e.err }

// mapDecodeError translates klauspost's errors into this package's.
func mapDecodeError(err error, in *source) error {
	switch {
	case err == nil || err == io.EOF:
		return err
	case in != nil && in.err != nil:
		return in.err
	case errors.Is(err, ErrOutputTooLarge):
		return ErrOutputTooLarge
	case errors.Is(err, io.ErrUnexpectedEOF):
		return io.ErrUnexpectedEOF
	// In streaming mode klauspost reports a window over the limit as either;
	// the output limit is counted here, so neither can mean that.
	case errors.Is(err, kzstd.ErrWindowSizeExceeded), errors.Is(err, kzstd.ErrDecoderSizeExceeded):
		return ErrWindowTooLarge
	case errors.Is(err, kzstd.ErrUnknownDictionary):
		return ErrDictionaryRequired
	case errors.Is(err, kzstd.ErrDecoderClosed):
		return ErrClosed
	}
	return hostCorruption{err}
}
