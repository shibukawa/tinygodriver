//go:build !fasthttp_nozstd && !tinygo && !force_tinygo_logic

// PETITWEB: the standard-Go half of zstd, and upstream's own. TinyGo takes
// zstd_tinygo.go instead, because klauspost's decoder is written in assembly
// that TinyGo cannot link; -tags fasthttp_nozstd takes zstd_disabled.go. See
// PATCHES.md.

package fasthttp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
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

// PETITWEB: zstdAvailable gates fasthttp's own use of zstd and
// zstdDecodeAvailable the half of it that reads; zstdReader lets fs.go name the
// decoder without importing it. This build has both halves.
const (
	zstdAvailable       = true
	zstdDecodeAvailable = true
)

type zstdReader = zstd.Decoder

// PETITWEB: ErrZstdUnsupported is declared in every build so that
// errors.Is against it compiles everywhere. Nothing returns it here; the
// builds that do are zstd_tinygo.go, which cannot decode, and
// zstd_disabled.go, which does neither.
var ErrZstdUnsupported = errors.New("fasthttp: zstd is unsupported in this build")

var (
	zstdDecoderPool            sync.Pool
	realZstdWriterPoolMap      = newCompressWriterPoolMap()
	stacklessZstdWriterPoolMap = newCompressWriterPoolMap()
)

func acquireZstdReader(r io.Reader) (*zstd.Decoder, error) {
	v := zstdDecoderPool.Get()
	if v == nil {
		return zstd.NewReader(r)
	}
	zr := v.(*zstd.Decoder) //nolint:forcetypeassert
	if err := zr.Reset(r); err != nil {
		return nil, err
	}
	return zr, nil
}

func releaseZstdReader(zr *zstd.Decoder) {
	zstdDecoderPool.Put(zr)
}

func acquireStacklessZstdWriter(w io.Writer, compressLevel int) stackless.Writer {
	nLevel := normalizeZstdCompressLevel(compressLevel)
	p := stacklessZstdWriterPoolMap[nLevel]
	v := p.Get()
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
	nLevel := normalizeZstdCompressLevel(level)
	p := stacklessZstdWriterPoolMap[nLevel]
	p.Put(zf)
}

func acquireRealZstdWriter(w io.Writer, level int) *zstd.Encoder {
	nLevel := normalizeZstdCompressLevel(level)
	p := realZstdWriterPoolMap[nLevel]
	v := p.Get()
	if v == nil {
		zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.EncoderLevel(nLevel)))
		if err != nil {
			panic(err)
		}
		return zw
	}
	zw := v.(*zstd.Encoder) //nolint:forcetypeassert
	zw.Reset(w)
	return zw
}

func releaseRealZstdWriter(zw *zstd.Encoder, level int) {
	zw.Close()
	nLevel := normalizeZstdCompressLevel(level)
	p := realZstdWriterPoolMap[nLevel]
	p.Put(zw)
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

// WriteUnzstd writes unzstd p to w and returns the number of uncompressed
// bytes written to w.
func WriteUnzstd(w io.Writer, p []byte) (int, error) {
	return writeUnzstd(w, p, 0)
}

func writeUnzstd(w io.Writer, p []byte, maxBodySize int) (int, error) {
	r := &byteSliceReader{b: p}
	zr, err := acquireZstdReader(r)
	if err != nil {
		return 0, err
	}
	n, err := copyZeroAllocWithLimit(w, zr, maxBodySize)
	releaseZstdReader(zr)
	nn := int(n)
	if int64(nn) != n {
		return 0, fmt.Errorf("too much data unzstd: %d", n)
	}
	return nn, err
}

// AppendUnzstdBytes appends unzstd src to dst and returns the resulting dst.
func AppendUnzstdBytes(dst, src []byte) ([]byte, error) {
	w := &byteSliceWriter{b: dst}
	_, err := WriteUnzstd(w, src)
	return w.b, err
}

// normalizes compression level into [0..7], so it could be used as an index
// in *PoolMap.
func normalizeZstdCompressLevel(level int) int {
	if level < CompressZstdSpeedNotSet || level > CompressZstdBestCompression {
		level = CompressZstdDefault
	}
	return level
}
