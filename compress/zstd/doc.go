// Package zstd writes RFC 8878 Zstandard frames and calculates cache metadata
// over the encoded representation while it is emitted.
//
// Host Go uses github.com/klauspost/compress/zstd. TinyGo uses this package's
// bounded encoder, which can also be selected on host Go with the shared
// force_tinygo_logic build tag. Both implementations expose the same API,
// calculate Result's SHA-256 digest over bytes successfully written, and write
// nothing to the destination until the caller writes, flushes, or closes.
package zstd
