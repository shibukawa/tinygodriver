package zstd

import _ "embed"

// Frames from the reference CLI, v1.5.7, so that `tinygo test` reaches the parts
// of the format only other encoders write; the host-Go tests generate far more
// on the fly, but TinyGo cannot run the CLI. The content is this package's
// encoder sources, concatenated, and all three frames carry XXH64 checksums.
//
//	zstd -6 --target-compressed-block-size=1024 src -o level6-1k-blocks.zst
//	zstd -19 src -o level19.zst
//	zstd -1 < src > level1-stdin.zst
//
// Small blocks are where the encoder reuses tables, so the first holds
// treeless literals, repeat-mode sequence tables and matches into earlier
// blocks. The second has level 19's deeper tables and -1 probabilities; the
// third, from stdin, states no content size and has blocks with over 16 KiB of
// literals, which take the five-byte literals header.
var (
	//go:embed testdata/decode/level6-1k-blocks.zst
	goldenSmallBlocks []byte
	//go:embed testdata/decode/level19.zst
	goldenLevel19 []byte
	//go:embed testdata/decode/level1-stdin.zst
	goldenStdin []byte
)

const (
	goldenSize   = 57705
	goldenSHA256 = "b8d5a98b82872d17a1c7a14fe59740c62f291ec49653bd1b155d04464bc2f4dc"
)
