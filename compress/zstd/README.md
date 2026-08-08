# compress/zstd

`compress/zstd` is an encoder-only RFC 8878 package for TinyGo and Go web
servers. It streams a valid `Content-Encoding: zstd` representation and
calculates its SHA-256 digest during output, so cache entries can retain the
encoded bytes and a strong ETag without hashing the bytes in a second pass.

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

## Implementation selection

- normal host Go builds use `github.com/klauspost/compress/zstd`
- TinyGo builds use this package's bounded pure-Go encoder
- `go build -tags force_tinygo_logic` forces the TinyGo-compatible encoder
  on host Go

Both implementations expose the same `Writer`, `Result`, `Option`, and
`EncodeAll` API.
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

- decoding
- dictionaries and the seekable format
- compression-level or dictionary options
- frame content checksums (the cache digest is separate)

The TinyGo backend additionally omits unsafe code, assembly, and CGo, and writes
Huffman weights only in the direct representation, never FSE-compressed. That
representation encodes its weight count as 127 plus it, so the largest literal
byte in a block must be 128 or below; a block whose literals reach higher stores
them instead of coding them. Binary payloads therefore compress through their
matches alone.
