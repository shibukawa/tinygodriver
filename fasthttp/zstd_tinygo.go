//go:build !fasthttp_nozstd && (tinygo || force_tinygo_logic)

// The TinyGo half of zstd. klauspost/compress/zstd decodes in hand-written
// arm64 and amd64 assembly, and TinyGo links neither: a plain `tinygo build`
// of upstream fasthttp fails at link time on buildDtable_asm and five
// sequenceDecs_* symbols. -tags noasm gets past that, at 2.53 MB of binary.
//
// This build encodes through the repository's own compress/zstd instead, which
// is pure Go and adds 0.05 MB, so `tinygo build` works with no tags at all.
// What it does not have is a decoder, so every read path here reports
// ErrZstdUnsupported and zstdDecodeAvailable keeps fasthttp itself away from
// them. See PATCHES.md.
//
// force_tinygo_logic selects this file on host Go, which is how the encoder is
// tested; the file is otherwise ordinary Go.

package fasthttp

import (
	"bytes"
	"errors"
	"io"
	"sync"

	"github.com/shibukawa/tinygodriver/compress/zstd"
	"github.com/shibukawa/tinygodriver/fasthttp/stackless"
	"github.com/valyala/bytebufferpool"
)

const (
	CompressZstdSpeedNotSet = iota
	CompressZstdBestSpeed
	CompressZstdDefault
	CompressZstdSpeedBetter
	CompressZstdBestCompression
)

// zstdAvailable reports whether this build can produce zstd, and
// zstdDecodeAvailable whether it can consume it. Splitting the two is the whole
// point of this file: responses compress, bodies and .zst cache files do not
// decompress.
const (
	zstdAvailable       = true
	zstdDecodeAvailable = false
)

// ErrZstdUnsupported reports that this build encodes zstd but cannot decode it.
var ErrZstdUnsupported = errors.New("fasthttp: zstd decoding is unavailable under TinyGo")

// zstdReader stands in for zstd.Decoder so that readFileHeader keeps its shape
// without naming a decoder that does not exist. acquireZstdReader never hands
// one out; the method is there for the type's shape rather than to be called.
type zstdReader struct{}

func (*zstdReader) Read(p []byte) (int, error) { return 0, ErrZstdUnsupported }

func acquireZstdReader(r io.Reader) (*zstdReader, error) { return nil, ErrZstdUnsupported }

func releaseZstdReader(zr *zstdReader) {}

// One pool per kind of writer, not one per compression level as in the
// standard-Go file: compress/zstd has a single encoder. Levels are still
// accepted and normalized so that application code compiles and behaves the
// same either way; they select nothing.
var (
	realZstdWriterPool      sync.Pool
	stacklessZstdWriterPool sync.Pool
)

func acquireStacklessZstdWriter(w io.Writer, compressLevel int) stackless.Writer {
	v := stacklessZstdWriterPool.Get()
	if v == nil {
		return stackless.NewWriter(w, func(w io.Writer) stackless.Writer {
			return acquireRealZstdWriter(w, compressLevel)
		})
	}
	sw := v.(stackless.Writer) //nolint:forcetypeassert
	sw.Reset(w)
	return sw
}

func releaseStacklessZstdWriter(zf stackless.Writer, level int) {
	zf.Close()
	stacklessZstdWriterPool.Put(zf)
}

// acquireRealZstdWriter returns a pooled encoder. *zstd.Writer satisfies
// stackless.Writer as it stands, and its Reset keeps the block buffer and match
// table that are the whole of its footprint, which is what makes pooling worth
// anything here.
func acquireRealZstdWriter(w io.Writer, level int) *zstd.Writer {
	v := realZstdWriterPool.Get()
	if v == nil {
		// WithETag(false): fasthttp has no use for the representation digest,
		// and calculating one would hash every response body twice over.
		zw, err := zstd.NewWriter(w, zstd.WithETag(false))
		if err != nil {
			panic(err)
		}
		return zw
	}
	zw := v.(*zstd.Writer) //nolint:forcetypeassert
	zw.Reset(w)
	return zw
}

func releaseRealZstdWriter(zw *zstd.Writer, level int) {
	zw.Close()
	realZstdWriterPool.Put(zw)
}

func AppendZstdBytesLevel(dst, src []byte, level int) []byte {
	w := &byteSliceWriter{b: dst}
	WriteZstdLevel(w, src, level) //nolint:errcheck
	return w.b
}

func WriteZstdLevel(w io.Writer, p []byte, level int) (int, error) {
	level = normalizeZstdCompressLevel(level)
	switch w.(type) {
	case *byteSliceWriter,
		*bytes.Buffer,
		*bytebufferpool.ByteBuffer:
		ctx := &compressCtx{
			w:     w,
			p:     p,
			level: level,
		}
		stacklessWriteZstd(ctx)
		return len(p), nil
	default:
		zw := acquireStacklessZstdWriter(w, level)
		n, err := zw.Write(p)
		releaseStacklessZstdWriter(zw, level)
		return n, err
	}
}

var (
	stacklessWriteZstdOnce sync.Once
	stacklessWriteZstdFunc func(ctx any) bool
)

func stacklessWriteZstd(ctx any) {
	stacklessWriteZstdOnce.Do(func() {
		stacklessWriteZstdFunc = stackless.NewFunc(nonblockingWriteZstd)
	})
	stacklessWriteZstdFunc(ctx)
}

func nonblockingWriteZstd(ctxv any) {
	ctx := ctxv.(*compressCtx) //nolint:forcetypeassert
	zw := acquireRealZstdWriter(ctx.w, ctx.level)
	zw.Write(ctx.p) //nolint:errcheck
	releaseRealZstdWriter(zw, ctx.level)
}

// AppendZstdBytes appends zstd src to dst and returns the resulting dst.
func AppendZstdBytes(dst, src []byte) []byte {
	return AppendZstdBytesLevel(dst, src, CompressZstdDefault)
}

// WriteUnzstd reports ErrZstdUnsupported. A client that receives a zstd body in
// this build finds out here rather than by decoding garbage.
func WriteUnzstd(w io.Writer, p []byte) (int, error) { return 0, ErrZstdUnsupported }

func writeUnzstd(w io.Writer, p []byte, maxBodySize int) (int, error) {
	return 0, ErrZstdUnsupported
}

// AppendUnzstdBytes reports ErrZstdUnsupported.
func AppendUnzstdBytes(dst, src []byte) ([]byte, error) { return dst, ErrZstdUnsupported }

// normalizes compression level into [0..7]. compress/zstd has one level, so
// this only keeps out-of-range values from reaching an API that documents them.
func normalizeZstdCompressLevel(level int) int {
	if level < CompressZstdSpeedNotSet || level > CompressZstdBestCompression {
		level = CompressZstdDefault
	}
	return level
}
