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
- a low-memory compressed level using one LZ match and RLE sequence tables
- streaming output with at most one input block retained
- `Flush` at block boundaries without ending the frame
- SHA-256 and encoded size calculated over bytes successfully written
- strong, quoted ETag formatting for the encoded representation

## Public API exclusions

- decoding
- dictionaries and the seekable format
- compression-level or dictionary options
- frame content checksums (the cache digest is separate)

The TinyGo backend additionally omits Huffman literals, general FSE tables,
multi-match blocks, unsafe code, assembly, and CGo.

Content without a useful match can become slightly larger. This basic level is
optimized for small implementation size and bounded TinyGo memory rather than
compression-ratio parity with general-purpose Zstandard encoders.
