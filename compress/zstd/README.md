# compress/zstd

`compress/zstd` is an RFC 8878 package for TinyGo and Go web servers. It
streams a valid `Content-Encoding: zstd` representation and calculates its
SHA-256 digest during output, so cache entries can retain the encoded bytes and
a strong ETag without hashing the bytes in a second pass. It also decodes zstd
from any encoder; see [Decoding](#decoding).

```go
encoded, result, err := zstd.EncodeAll(body)
if err != nil {
	return err
}
header.Set("Content-Encoding", zstd.ContentEncoding)
header.Set("ETag", result.ETag())
```

ETag calculation is enabled by default. Disable it for responses such as
`Cache-Control: no-store`; this avoids allocating and updating SHA-256:

```go
encoded, result, err := zstd.EncodeAll(body, zstd.WithETag(false))
// result.ETagEnabled is false, result.ETag() is empty, and SHA256 is zero.
```

`NewWriter` provides the bounded streaming form. Call `Close`, then `Result`;
closing the encoder does not close its destination.

Constructing an encoder writes nothing to the destination. As in
`compress/gzip`, the frame header goes out with the first `Write`, `Flush`, or
`Close`. A handler can therefore wrap its `http.ResponseWriter` before
rendering and still answer a rendering failure with an uncompressed error
response: nothing has reached the wire, so the status is not committed and
`Content-Encoding` can still be dropped.

`Flush` emits the buffered input as complete blocks so a reader can decode
everything written so far, which is what streaming responses and server-sent
events need between chunks. It neither ends the frame nor flushes the
destination, so flush the destination separately:

```go
if _, err := z.Write(chunk); err != nil {
	return err
}
if err := z.Flush(); err != nil {
	return err
}
w.(http.Flusher).Flush()
```

Flushing before a block fills reduces the compression ratio, so flush per
chunk rather than per `Write`.

`Reset` starts a new frame on a new destination, so a server can pool encoders
across responses instead of building one per response. It keeps what the
encoder is made of — under TinyGo a 128 KiB block buffer and a 16 KiB match
table, which is nearly all of its footprint — and keeps the `WithETag` setting
chosen at `NewWriter`. As with `NewWriter`, nothing reaches the new destination
until the caller writes, flushes, or closes.

```go
z := pool.Get().(*zstd.Writer)
z.Reset(w)
defer func() { z.Close(); pool.Put(z) }()
```

[`fasthttp`](../../fasthttp) does exactly this: it is the encoder TinyGo builds
of that fork compress with, because klauspost's decoder is assembly TinyGo
cannot link.

## Decoding

`DecodeAll` decodes a whole body; `Reader` decodes a stream a block at a time.
Both accept every frame RFC 8878 defines except those that need a dictionary,
skip skippable frames, run on across concatenated frames, and verify content
checksums when a frame carries one.

```go
body, err := zstd.DecodeAll(nil, encoded, zstd.WithMaxOutput(10<<20))
```

Input from the network needs two limits, and the decoder applies both:

- **Window.** A frame declares how much earlier content its matches may
  reach, and a decoder has to keep that much. A frame declaring more than
  `WithMaxWindow` fails with `ErrWindowTooLarge` before anything is decoded.
  The default is 8 MiB, the limit [RFC 9659](https://www.rfc-editor.org/rfc/rfc9659)
  sets for the zstd content coding and within which the reference CLI stays up
  to level 19. A `Reader` holds at most twice the window plus one 128 KiB
  block, so this bounds its memory.
- **Output.** A few kilobytes of zstd can decode to gigabytes. `WithMaxOutput`
  fails with `ErrOutputTooLarge` once the content passes a length; a `Reader`
  returns the content up to it first. There is no default, so set it for
  untrusted input.

`NewReader` reads nothing until the first `Read`, so it fails only on an
invalid option. `Reset` keeps a `Reader`'s buffers for pooling, and `Close`
releases them without closing the underlying reader. A pool can build its
Readers without a stream and hand them one with `Reset`:

```go
var readers = sync.Pool{New: func() any {
	r, _ := zstd.NewReader(nil, zstd.WithMaxOutput(10<<20))
	return r
}}

r := readers.Get().(*zstd.Reader)
defer readers.Put(r)
if err := r.Reset(body); err != nil {
	return err
}
_, err := io.Copy(dst, r)
```

Failures are reported the same way by both implementations:

| error | meaning |
|---|---|
| `io.ErrUnexpectedEOF` | the input ends inside a frame |
| `ErrCorrupt` | anything else malformed, including a checksum mismatch; the wrapping error says where |
| `ErrWindowTooLarge` | the frame's window exceeds `WithMaxWindow` |
| `ErrOutputTooLarge` | the content exceeds `WithMaxOutput` |
| `ErrDictionaryRequired` | the frame was compressed against a dictionary |

A read error from the underlying stream is returned as it is.

The TinyGo decoder is as strict as the reference implementation where
klauspost is lenient: an entropy-coded bitstream must end exactly where its
last symbol does, and a block may not exceed the smaller of the window and
128 KiB. The two implementations can therefore disagree on a damaged frame that
no encoder would write, but not on anything an encoder does write.

Measured on an Apple M-series machine, over the reference CLI's frames of this
package's own sources:

| decoder | throughput |
|---|---|
| TinyGo decoder, host Go | 270–450 MB/s |
| TinyGo decoder, TinyGo 0.42 | ~250 MB/s |
| klauspost through this API, host Go | 510–960 MB/s |

Neither allocates per call once its pools are warm. Under TinyGo the decoder
adds about 92 KB to a program that uses `DecodeAll` and `Reader`, and nothing
to one that only encodes.

## Implementation selection

- normal host Go builds use `github.com/klauspost/compress/zstd`
- TinyGo builds use this package's bounded pure-Go encoder and decoder
- `go build -tags force_tinygo_logic` forces the TinyGo-compatible code
  on host Go

Both implementations expose the same `Writer`, `Result`, `Option`, `EncodeAll`,
`Reader`, `DecoderOption` and `DecodeAll` API, `Reset` included.
Encoded bytes and therefore ETags may differ between implementations.

The host backend uses the klauspost default compression level with one encoder,
a 128 KiB window, lower-memory mode, and no frame checksum. The TinyGo
backend has the following supported subset:

- standard Zstandard frames with a 128 KiB window
- raw and RLE blocks of at most 128 KiB, including profitable interior runs
- compressed blocks carrying many sequences, from a greedy matcher that keeps
  one candidate per hash slot
- FSE sequence tables fitted to each block, falling back to the format's
  predefined tables when a block has too few sequences to pay for a description,
  and to RLE tables when a stream carries one symbol
- repeat offsets, for the common case of a match at the previous distance
- a lazy step, which defers a match by one byte when the next position starts a
  longer one
- Huffman-coded literals, in one stream or four, with the direct weight
  representation; raw and RLE literal blocks are used where either is smaller
- streaming output with at most one input block retained
- `Flush` at block boundaries without ending the frame
- SHA-256 and encoded size calculated over bytes successfully written
- strong, quoted ETag formatting for the encoded representation

Every block falls back to raw or RLE when a compressed one would not be smaller,
so output never exceeds the input by more than the block headers.

## Compression ratio

Measured against `compress/flate` at its default level, which is the encoding a
server would otherwise negotiate:

| payload | this encoder | deflate |
|---|---|---|
| 14 KiB HTML listing | 8.2% | 11.6% |
| 11 KiB JSON array | 11.0% | 13.3% |
| 5 KiB varied text | 29.6% | 26.6% |
| one repeated string | 1.4% | 1.6% |
| incompressible | 100.1% | 100.1% |

Varied prose is the one case that loses, and the breakdown says why: its cost is
1247 bytes of sequences against 233 of literals, where deflate is finding
word-level repeats this matcher does not.

`TestRatioAgainstDeflate` holds these within a stated multiple of deflate, and
every case in the suite decodes through the reference implementation, so no ratio
here was bought with bytes a real decoder would reject.

Matching stays inside the current block, which is what bounds memory to one
retained block. A match therefore never reaches back into an earlier block, even
though the window would allow it, so a payload whose repeats are further apart
than 128 KiB compresses worse than a general-purpose encoder would manage.

## Public API exclusions

- dictionaries and the seekable format, for encoding and decoding
- compression-level or dictionary options
- writing frame content checksums (the cache digest is separate); the
  decoder verifies them

The TinyGo backend additionally omits unsafe code, assembly, and CGo, and writes
Huffman weights only in the direct representation, never FSE-compressed. That
representation encodes its weight count as 127 plus it, so the largest literal
byte in a block must be 128 or below; a block whose literals reach higher stores
them instead of coding them. Binary payloads therefore compress through their
matches alone.
