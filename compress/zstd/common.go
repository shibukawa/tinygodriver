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
	// ErrClosed reports use of a Writer or Reader after Close.
	ErrClosed            = errors.New("zstd: use after close")
	ErrResultUnavailable = errors.New("zstd: result is unavailable before a successful close")

	// errNilWriter is reported by NewWriter and held by Reset, whose signature
	// has nowhere to return it.
	errNilWriter = errors.New("zstd: nil writer")
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
	// Web payloads land around half their size; guessing low once beats letting
	// the buffer climb its growth chain from empty.
	dst.Grow(len(src)/2 + 64)
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
	Reset()
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

// reset points the writer at a new destination and starts its size and digest
// over. The hasher is kept rather than replaced, so a reused encoder does not
// allocate one per representation.
func (w *outputWriter) reset(dst io.Writer) {
	w.dst = dst
	w.size = 0
	if w.hash != nil {
		w.hash.Reset()
	}
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

// Decoding. Both backends decode through this API and report failures with
// these errors, so code written against one behaves the same on the other.

var (
	// ErrCorrupt reports input that is not valid Zstandard: a bad magic
	// number, a malformed block, a match reaching outside the output, a
	// content size or checksum that does not match. Errors that wrap it say
	// where the input went wrong. Input that simply ends early is reported as
	// io.ErrUnexpectedEOF instead.
	ErrCorrupt = errors.New("zstd: corrupt input")

	// ErrWindowTooLarge reports a frame that declares a window larger than
	// the decoder accepts; see WithMaxWindow. It is checked before the frame
	// is decoded, so it costs no memory.
	ErrWindowTooLarge = errors.New("zstd: frame window exceeds the limit")

	// ErrOutputTooLarge reports content longer than WithMaxOutput allows.
	ErrOutputTooLarge = errors.New("zstd: decoded content exceeds the limit")

	// ErrDictionaryRequired reports a frame compressed against a dictionary,
	// which this package does not support.
	ErrDictionaryRequired = errors.New("zstd: frame requires a dictionary")

	errNilReader        = errors.New("zstd: nil reader")
	errInvalidMaxWindow = errors.New("zstd: WithMaxWindow must be between 1 KiB and 1 GiB")
	errInvalidMaxOutput = errors.New("zstd: WithMaxOutput must not be negative")
)

const (
	// defaultMaxWindow is RFC 9659's limit for the zstd content coding: a
	// sender must not use a larger window, and a recipient may refuse one.
	// The reference CLI stays within it up to level 19.
	defaultMaxWindow = 8 << 20

	// maxWindowCeiling bounds WithMaxWindow. It is the reference decoder's
	// limit on 32-bit platforms, and it keeps every window size an int on
	// TinyGo's wasm targets.
	maxWindowCeiling = 1 << 30

	minWindow = 1 << 10
)

// DecoderOption configures DecodeAll and NewReader.
type DecoderOption interface {
	applyDecoder(decoderOptions) decoderOptions
}

// decoderOptionFunc takes and returns the options by value: through a pointer,
// the interface call would move them to the heap on every DecodeAll.
type decoderOptionFunc func(decoderOptions) decoderOptions

func (f decoderOptionFunc) applyDecoder(options decoderOptions) decoderOptions { return f(options) }

type decoderOptions struct {
	maxWindow int
	maxOutput int64 // 0 means no limit
	err       error
}

// WithMaxWindow sets the largest window a frame may declare, from 1 KiB to
// 1 GiB. The default is 8 MiB, the limit RFC 9659 sets for HTTP. A Reader
// keeps up to twice the window in memory, so this is what bounds its
// footprint; a larger frame fails with ErrWindowTooLarge before it is decoded.
func WithMaxWindow(n int) DecoderOption {
	return decoderOptionFunc(func(options decoderOptions) decoderOptions {
		if n < minWindow || n > maxWindowCeiling {
			options.err = errInvalidMaxWindow
		} else {
			options.maxWindow = n
		}
		return options
	})
}

// WithMaxOutput limits the decoded content to n bytes across all frames;
// longer content fails with ErrOutputTooLarge, after a Reader has returned the
// first n bytes. Zero, the default, sets no limit. Without one, a few
// kilobytes of input can decode to gigabytes, so set it for input from
// untrusted sources.
func WithMaxOutput(n int64) DecoderOption {
	return decoderOptionFunc(func(options decoderOptions) decoderOptions {
		if n < 0 {
			options.err = errInvalidMaxOutput
		} else {
			options.maxOutput = n
		}
		return options
	})
}

func resolveDecoderOptions(options []DecoderOption) (decoderOptions, error) {
	resolved := decoderOptions{maxWindow: defaultMaxWindow}
	for _, option := range options {
		if option != nil {
			resolved = option.applyDecoder(resolved)
		}
	}
	return resolved, resolved.err
}
