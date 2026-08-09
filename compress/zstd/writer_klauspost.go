//go:build !tinygo && !force_tinygo_logic

package zstd

import (
	"errors"
	"io"

	kzstd "github.com/klauspost/compress/zstd"
)

// Writer emits one Zstandard frame through github.com/klauspost/compress/zstd.
// Writer is not safe for concurrent use. Close must succeed before Result can
// be read.
type Writer struct {
	encoder *kzstd.Encoder
	out     *outputWriter
	closed  bool
	err     error
}

// NewWriter starts a host-Go Zstandard stream. Constructing a Writer writes
// nothing to the destination, so an encoder built and then abandoned leaves the
// destination untouched. TinyGo and builds using the force_tinygo_logic tag
// select the bounded TinyGo encoder instead.
func NewWriter(w io.Writer, options ...Option) (*Writer, error) {
	if w == nil {
		return nil, errors.New("zstd: nil writer")
	}
	resolved := resolveOptions(options)
	out := newOutputWriter(w, resolved.etag)
	encoder, err := kzstd.NewWriter(out,
		kzstd.WithEncoderLevel(kzstd.SpeedDefault),
		kzstd.WithEncoderConcurrency(1),
		kzstd.WithWindowSize(128<<10),
		kzstd.WithLowerEncoderMem(true),
		kzstd.WithEncoderCRC(false),
	)
	if err != nil {
		return nil, err
	}
	return &Writer{encoder: encoder, out: out}, nil
}

func (z *Writer) Write(p []byte) (int, error) {
	if z.closed {
		return 0, ErrClosed
	}
	if z.err != nil {
		return 0, z.err
	}
	n, err := z.encoder.Write(p)
	if err != nil {
		z.err = err
	}
	return n, err
}

// Flush emits the buffered input as complete blocks so that everything written
// so far can be decoded, and returns once those bytes reach the destination.
// It does not end the frame and it does not flush the destination itself.
// Flushing before a block fills reduces the compression ratio.
func (z *Writer) Flush() error {
	if z.closed {
		return ErrClosed
	}
	if z.err != nil {
		return z.err
	}
	if err := z.encoder.Flush(); err != nil {
		z.err = err
		return err
	}
	return nil
}

// Close finishes the frame. It does not close the destination.
func (z *Writer) Close() error {
	if z.closed {
		return z.err
	}
	z.closed = true
	if err := z.encoder.Close(); z.err == nil {
		z.err = err
	}
	return z.err
}

// Result returns the encoded size and SHA-256 digest after a successful Close.
func (z *Writer) Result() (Result, error) {
	if !z.closed || z.err != nil {
		return Result{}, ErrResultUnavailable
	}
	return z.out.result(), nil
}
