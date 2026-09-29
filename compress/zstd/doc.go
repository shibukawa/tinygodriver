// Package zstd writes RFC 8878 Zstandard frames, calculating cache metadata
// over the encoded representation while it is emitted, and reads them back.
//
// Host Go uses github.com/klauspost/compress/zstd. TinyGo uses this package's
// own bounded encoder and decoder, which can also be selected on host Go with
// the shared force_tinygo_logic build tag. Both implementations expose the
// same API. The encoders calculate Result's SHA-256 digest over bytes
// successfully written and write nothing to the destination until the caller
// writes, flushes, or closes. The decoders accept any frame without a
// dictionary, report failures with the same errors, and refuse windows beyond
// WithMaxWindow, 8 MiB by default.
//
// Writer and Reader are poolable through Reset, which is how the fasthttp
// fork in this repository compresses responses under TinyGo.
package zstd
