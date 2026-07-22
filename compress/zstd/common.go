package zstd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// ContentEncoding is the HTTP content-coding token for Zstandard.
const ContentEncoding = "zstd"

var (
	ErrClosed            = errors.New("zstd: writer is closed")
	ErrResultUnavailable = errors.New("zstd: result is unavailable before a successful close")
)

// Result describes an encoded representation. SHA256 covers exactly Size
// bytes written to the destination, including the Zstandard frame headers.
type Result struct {
	Size        int64
	SHA256      [sha256.Size]byte
	ETagEnabled bool
}

// ETag returns a quoted strong HTTP entity-tag for the encoded representation.
// It returns an empty string when the encoder used WithETag(false).
func (r Result) ETag() string {
	if !r.ETagEnabled {
		return ""
	}
	return `"sha256-` + hex.EncodeToString(r.SHA256[:]) + `"`
}

// Option configures both the host-Go and TinyGo encoders.
type Option interface {
	apply(*writerOptions)
}

type optionFunc func(*writerOptions)

func (f optionFunc) apply(options *writerOptions) { f(options) }

type writerOptions struct {
	etag bool
}

// WithETag controls whether SHA-256 cache metadata is calculated while the
// encoded representation is written. It is enabled by default. When disabled,
// Result.SHA256 is zero and Result.ETag returns an empty string.
func WithETag(enabled bool) Option {
	return optionFunc(func(options *writerOptions) { options.etag = enabled })
}

func resolveOptions(options []Option) writerOptions {
	resolved := writerOptions{etag: true}
	for _, option := range options {
		if option != nil {
			option.apply(&resolved)
		}
	}
	return resolved
}

// EncodeAll encodes src, returning the frame and its cache metadata. The
// digest is produced while the frame is written; the encoded bytes are not
// traversed a second time.
func EncodeAll(src []byte, options ...Option) ([]byte, Result, error) {
	var dst bytes.Buffer
	z, err := NewWriter(&dst, options...)
	if err != nil {
		return nil, Result{}, err
	}
	if _, err := z.Write(src); err != nil {
		return nil, Result{}, err
	}
	if err := z.Close(); err != nil {
		return nil, Result{}, err
	}
	result, err := z.Result()
	if err != nil {
		return nil, Result{}, err
	}
	return dst.Bytes(), result, nil
}

type outputWriter struct {
	dst  io.Writer
	hash hashState
	size int64
}

type hashState interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func newOutputWriter(dst io.Writer, etag bool) *outputWriter {
	w := &outputWriter{dst: dst}
	if etag {
		w.hash = sha256.New()
	}
	return w
}

func (w *outputWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) != 0 {
		n, err := w.dst.Write(p)
		if n < 0 || n > len(p) {
			return total, errors.New("zstd: invalid writer count")
		}
		if n != 0 {
			if w.hash != nil {
				_, _ = w.hash.Write(p[:n])
			}
			w.size += int64(n)
			total += n
			p = p[n:]
		}
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (w *outputWriter) result() Result {
	var result Result
	result.Size = w.size
	result.ETagEnabled = w.hash != nil
	if w.hash != nil {
		copy(result.SHA256[:], w.hash.Sum(nil))
	}
	return result
}
