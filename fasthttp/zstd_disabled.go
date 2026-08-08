//go:build fasthttp_nozstd

// The zstd-free half of the package. klauspost/compress/zstd costs 2.40 MB of
// TinyGo binary on its own -- more than half of what fasthttp adds over
// net/http, and roughly ten times brotli's 0.24 MB -- so a program that will
// never negotiate zstd can leave it out with -tags fasthttp_nozstd.
//
// Nothing inside fasthttp reaches the writers below in this build: zstdAvailable
// keeps it out of both CompressHandler switches and out of FS's encoding
// choice, so a client asking only for zstd is served identity, which is what
// content negotiation is for. What remains is the exported API, kept whole so
// that application code compiles either way. See PATCHES.md.

package fasthttp

import (
	"errors"
	"io"

	"github.com/shibukawa/tinygodriver/fasthttp/stackless"
)

// zstdAvailable reports whether this build can produce or consume zstd.
const zstdAvailable = false

const (
	CompressZstdSpeedNotSet = iota
	CompressZstdBestSpeed
	CompressZstdDefault
	CompressZstdSpeedBetter
	CompressZstdBestCompression
)

// ErrZstdUnsupported reports that this build excluded zstd.
var ErrZstdUnsupported = errors.New("fasthttp: built with -tags fasthttp_nozstd")

// zstdReader stands in for zstd.Decoder, so that readFileHeader keeps its shape
// without naming the excluded package. It reads as an io.Reader because that is
// what readFileHeader assigns it to; acquireZstdReader never hands one out, so
// the method is there for the type's shape rather than to be called.
type zstdReader struct{}

func (*zstdReader) Read(p []byte) (int, error) { return 0, ErrZstdUnsupported }

func acquireZstdReader(r io.Reader) (*zstdReader, error) { return nil, ErrZstdUnsupported }

func releaseZstdReader(zr *zstdReader) {}

// acquireStacklessZstdWriter returns a writer that refuses every write. It is
// unreachable from fasthttp itself in this build; only a caller that reimplements
// the negotiation can arrive here, and silently emitting cleartext under a zstd
// Content-Encoding would be worse than an error.
func acquireStacklessZstdWriter(w io.Writer, compressLevel int) stackless.Writer {
	return failingWriter{err: ErrZstdUnsupported}
}

func releaseStacklessZstdWriter(zf stackless.Writer, level int) {}

// failingWriter is a stackless.Writer that reports err from every operation that
// can report one.
type failingWriter struct{ err error }

func (f failingWriter) Write(p []byte) (int, error) { return 0, f.err }
func (f failingWriter) Flush() error                { return f.err }
func (f failingWriter) Close() error                { return f.err }
func (f failingWriter) Reset(w io.Writer)           {}

// AppendZstdBytesLevel panics, because its signature cannot report an error and
// returning dst unchanged would hand back an empty body for a caller to label
// zstd. Reaching it means asking a zstd-free build to compress: a build
// configuration mistake, not a runtime condition.
func AppendZstdBytesLevel(dst, src []byte, level int) []byte {
	panic(ErrZstdUnsupported)
}

// AppendZstdBytes panics; see AppendZstdBytesLevel.
func AppendZstdBytes(dst, src []byte) []byte {
	return AppendZstdBytesLevel(dst, src, CompressZstdDefault)
}

func WriteZstdLevel(w io.Writer, p []byte, level int) (int, error) {
	return 0, ErrZstdUnsupported
}

// WriteUnzstd reports ErrZstdUnsupported. A client that receives a zstd body in
// this build finds out here rather than by decoding garbage.
func WriteUnzstd(w io.Writer, p []byte) (int, error) { return 0, ErrZstdUnsupported }

func writeUnzstd(w io.Writer, p []byte, maxBodySize int) (int, error) {
	return 0, ErrZstdUnsupported
}

// AppendUnzstdBytes reports ErrZstdUnsupported.
func AppendUnzstdBytes(dst, src []byte) ([]byte, error) { return dst, ErrZstdUnsupported }

func normalizeZstdCompressLevel(level int) int {
	if level < CompressZstdSpeedNotSet || level > CompressZstdBestCompression {
		level = CompressZstdDefault
	}
	return level
}
