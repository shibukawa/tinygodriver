# SIMD and decoder improvements — 2026-10-20

The shared zstd decoder now interleaves its four independent Huffman streams.
TinyGo also reloads bit windows with an overlapping 64-bit read. Standard Go's
compact refill remains eligible for inlining: using the larger reload there
exceeded Go 1.27's inline budget and regressed sequence-heavy inputs. Zstd's
improvement comes from instruction-level parallelism and fewer refill operations.

The two WebSocket forks share a SIMD XOR kernel. XML delimiter search and
fasthttp header/trailer value validation use the new bounded byte kernels
(the latter retired with fasthttp v1.76.0; see below).
Go SIMD uses `simd/archsimd`; TinyGo uses Clang vectors. The public APIs and
accepted protocol bytes are preserved.

## Measurement conditions

Apple M3, darwin/arm64, Go 1.27.0, TinyGo 0.42.0 / LLVM 22.1.4. Each condition
has five 300 ms samples, measured sequentially with `GOMAXPROCS=1` and rotated
benchmark ordering. Tables show medians and the ratio of median throughput.
CPU affinity and other macOS workloads were not controlled. The raw log retains
the original 500 samples, including variation between runs. The hex section
adds a separate 300-sample run.

Zstd uses the same three 57,705-byte reference frames as the
[earlier baseline and CPU profiles](compress/zstd/BENCHMARKS.md). Both versions
verify the frame's XXH64 checksum. Destination buffers and decoders are warmed.
The other kernels reuse hot in-memory buffers. These are single-threaded
measurements and exclude socket, disk and network throughput.

The Go zstd columns use `-tags force_tinygo_logic`: they measure the shared
decoder, not the normal Go backend, which uses klauspost/compress. The earlier
baseline contains the separate klauspost comparison. The byte-kernel Go
columns use `GOEXPERIMENT=simd`; TinyGo columns use its default `-opt=z`.

## Zstd

Decoded decimal MB/s, before → after (speed ratio).

| Frame | Shared decoder / Go | TinyGo `-opt=z` | TinyGo `-opt=2` |
|---|---:|---:|---:|
| level6-1k-blocks | 417.2 → 453.6 (1.09×) | 266.1 → 268.1 (1.01×) | 360.4 → 368.6 (1.02×) |
| level19 | 457.9 → 544.9 (1.19×) | 274.5 → 292.9 (1.07×) | 376.0 → 399.9 (1.06×) |
| level1-stdin | 321.9 → 665.2 (2.07×) | 287.8 → 403.1 (1.40×) | 345.5 → 602.4 (1.74×) |

`Reader` uses the same decoding logic and shows similar gains:

| Frame | Shared decoder / Go | TinyGo `-opt=z` | TinyGo `-opt=2` |
|---|---:|---:|---:|
| level6-1k-blocks | 432.7 → 463.8 (1.07×) | 266.6 → 268.5 (1.01×) | 373.7 → 371.9 (1.00×) |
| level19 | 464.6 → 564.3 (1.21×) | 272.4 → 291.1 (1.07×) | 386.3 → 401.6 (1.04×) |
| level1-stdin | 319.3 → 663.9 (2.08×) | 289.6 → 405.9 (1.40×) | 350.8 → 611.0 (1.74×) |

All zstd samples report 0 B/op and 0 allocs/op after warm-up. Small-block data
has little improvement under TinyGo; the largest gain is on level-1 frames,
where Huffman literals dominated the original Go CPU profile. The corpus is
one small source document with three encodings, not a general archive corpus.

## WebSocket masking

One mask pass over a hot buffer, misaligned by four bytes. Nanoseconds per
operation, before → after (throughput ratio). Both WebSocket forks use the
same kernel; these timings use the `websocket` fork's existing benchmark.

| Bytes | Go SIMD | TinyGo vectors |
|---|---:|---:|
| 16 | 9.71 → 10.14 (0.96×) | 9.70 → 10.44 (0.93×) |
| 32 | 11.00 → 11.34 (0.97×) | 11.19 → 11.69 (0.96×) |
| 512 | 27.33 → 11.62 (2.35×) | 27.49 → 11.10 (2.48×) |
| 1024 | 49.98 → 20.48 (2.44×) | 49.45 → 19.88 (2.49×) |

The historical benchmark label `word` now invokes the selected implementation.
Only buffers of at least 64 bytes select SIMD. The 16- and 32-byte cases retain
the scalar algorithm and show about 0.3–0.7 ns of dispatch/code-layout overhead.
The 512- and 1024-byte cases improve about 2.4–2.5 times. These timings do not
include framing, compression or network traffic. Tests cover all key phases,
unaligned buffers, exact vector boundaries and split-mask position continuity.

## HTTP value validation

Nanoseconds per value: original inline table loop → selected vector path
(throughput ratio). Values contain valid visible ASCII. Short values use the
original inline loop in the request, response and trailer parsers.

| Value bytes | Go SIMD | TinyGo vectors |
|---|---:|---:|
| 16 | 5.80 → 5.77 (1.01×) | 6.86 → 6.84 (1.00×) |
| 64 | 18.86 → 6.21 (3.04×) | 26.13 → 4.50 (5.81×) |
| 1024 | 284.20 → 44.87 (6.33×) | 417.00 → 31.44 (13.26×) |
| 4096 | 1116.00 → 179.60 (6.21×) | 1658.00 → 127.70 (12.98×) |

This measures validation only, not full HTTP parsing or request throughput.
Every possible byte is compared with fasthttp's original validation table in
every vector/tail position; HTAB and obs-text remain accepted and DEL rejected.

Retired on 2026-10-24 with the move to fasthttp v1.76.0, whose scanner
validates each value in the same pass that finds the end of its line, so the
loops this replaced are gone; see [fasthttp/PATCHES.md](fasthttp/PATCHES.md).
The kernel itself remains in `internal/bytealg`.

## XML delimiter search

TinyGo byte search for an absent delimiter, standard `bytes.IndexByte` → new
bounded SIMD search. Nanoseconds per search (throughput ratio).

| Bytes | TinyGo |
|---|---:|
| 32 | 15.88 → 16.21 (0.98×) |
| 256 | 113.90 → 15.66 (7.27×) |
| 4096 | 1664.00 → 151.80 (10.96×) |

Standard Go continues to use its existing optimized `bytes.IndexByte`.
Complete scans of the 1.5 MB worksheet benchmark show essentially no change:

| Scan | Go MB/s before → after | TinyGo MB/s before → after |
|---|---:|---:|
| Reader | 418.0 → 418.9 (1.00×) | 238.1 → 237.7 (1.00×) |
| BytesReader | 420.9 → 418.3 (0.99×) | 240.3 → 240.4 (1.00×) |

The worksheet contains many short text segments and significant name and
attribute work; its results do not establish a whole-parser speedup. The
search-kernel gains apply to longer spans. XML refill offsets and token
boundaries are covered by the existing reader suite on both compilers.

## Hexadecimal conversion

The new `encoding/simdhex` package (named `encoding/hex` until 2026-10-25) provides the standard hex API, including mixed
case decoding, error identity and partial output. AWS signing, S3 hashes,
zstd ETags and the vendored PostgreSQL helpers now use it. PostgreSQL bytea
also appends directly into its destination instead of making a temporary
hex string. Its regeneration script reproduces all 15 modified vendored files.

**Update 2026-10-25.** Go on arm64 now selects a Go-assembler NEON backend
(`encoding/simdhex/vector_arm64.s`) in default builds, replacing the `archsimd`
kernel measured below, which needed `GOEXPERIMENT=simd`; the `purego` tag
restores the standard library and amd64 keeps `archsimd`. Under the same
rotated five-sample protocol, 64 KiB encoding went from 19.77 to 36.12 GB/s
and decoding from 8.44 to 16.17 GB/s, ahead of the TinyGo C kernel's 24.2 and
9.5 GB/s; the 32-byte case went from 4.60 to 4.03 ns encoding and from 7.27 to
5.91 ns decoding. [The hex README](encoding/simdhex/README.md) has the full tables
and [the new samples](encoding/simdhex/testdata/bench-20261025.txt); the tables
below remain the 2026-10-20 `archsimd` run.

These are 300 additional samples from the same Apple M3 with five 300 ms runs
per condition and rotated compiler order. Each binary compares its own
standard library with the selected implementation. Throughput counts raw bytes
for both operations. Go uses `GOEXPERIMENT=simd`; TinyGo uses the C vector
backend. All samples report 0 B/op and 0 allocs/op with reused output buffers.

64 KiB binary input, decimal GB/s: standard → selected (throughput ratio).

| Operation | Go SIMD | TinyGo `-opt=z` | TinyGo `-opt=2` |
|---|---:|---:|---:|
| Encode | 1.73 → 17.64 (10.18×) | 1.85 → 24.35 (13.20×) | 1.86 → 24.31 (13.06×) |
| Decode | 1.54 → 7.74 (5.04×) | 1.50 → 9.55 (6.39×) | 1.24 → 9.56 (7.74×) |

32-byte binary input (the size of SHA-256 digests), ns/op:

| Operation | Go SIMD | TinyGo `-opt=z` | TinyGo `-opt=2` |
|---|---:|---:|---:|
| Encode | 20.02 → 4.55 (4.40×) | 19.97 → 7.21 (2.77×) | 15.80 → 2.93 (5.39×) |
| Decode | 20.84 → 7.21 (2.89×) | 23.23 → 8.01 (2.90×) | 26.64 → 4.28 (6.23×) |

The switching threshold is 32 raw bytes (64 encoded characters for decoding).
16-byte cases retain the scalar codec and show up to about 1.7 ns of extra
wrapper/dispatch cost under TinyGo's default size optimization; `-opt=2`
keeps those cases close to the standard library. These are buffer-kernel
measurements, not complete signing, storage or PostgreSQL throughput.

The standard, Go SIMD, Clang and TinyGo compatibility suites passed. The Go
SIMD and C fuzz runs completed 871,356 and 1,474,394 cases. Exact-sized C
buffers passed AddressSanitizer and UndefinedBehaviorSanitizer; amd64 binaries
ran under Rosetta and Go/TinyGo WASI fallbacks compiled. AWS/S3/zstd caller
regressions and the vendored bytea regression passed on both Go and TinyGo.
Temporary vendor fixtures reproduced all 271 Go files across the two
WebSocket forks, fasthttp and vendored PostgreSQL, including the local SIMD
regression tests.

See [the hex package](encoding/simdhex/README.md) for API and build details, and
[all 300 hex samples](encoding/simdhex/testdata/bench-20261020.txt) for observed
variability. Benchmarks use `BenchmarkHex` with the same flags as the other
kernels. The streaming and dump adapters delegate to the standard library.

## Backend selection and verification

The shared [byte kernels](internal/bytealg/README.md) use NEON on arm64.
Go's amd64 path checks AVX2 at runtime; the C amd64 path requires only baseline
SSE2. Ordinary Go, unsupported architectures, and TinyGo builds without cgo
retain their scalar/standard-library paths. All loads and stores remain inside
the provided slice's length.

Verification completed:

- Standard Go and Go SIMD: `internal/bytealg`, both WebSocket forks, XML and
  fasthttp; normal Go and `force_tinygo_logic` zstd tests.
- TinyGo: byte kernels, both WebSocket forks, XML, fasthttp, and zstd at both
  `-opt=z` and `-opt=2`. macOS network integration tests use SDK 26.5 linker paths.
- Independent bit-at-a-time oracle for zstd refill, all end-marker positions,
  0–32-bit reads, short tails and overread detection; interleaved Huffman tests
  include uneven segment sizes, variable code lengths and corrupted streams.
- Zstd reference-comparison fuzzing: 139,272 executions in a 15-second run.
  The harness limits output to 1 MiB and accounts for the different precedence
  of streaming truncation and declared-size limit failures.
- Clang backend under host Go; exact-size C allocations under AddressSanitizer
  and UndefinedBehaviorSanitizer.
- amd64 Go SIMD and C backend binaries built and executed under Rosetta.
  This is not a native amd64 performance measurement. WASI fallback compiled.

## Reproduction

```sh
GOEXPERIMENT=simd go test ./internal/bytealg -run '^$' \
  -bench '^BenchmarkByteKernels$' -benchmem -benchtime=300ms -count=5
GOEXPERIMENT=simd go test ./websocket -run '^$' \
  -bench '^BenchmarkMaskBytes$/^size-(16|32|512|1024)$/^align-4$/^word$' \
  -benchmem -benchtime=300ms -count=5
go test -tags force_tinygo_logic ./compress/zstd -run '^$' \
  -bench '^BenchmarkDecode$' -benchmem -benchtime=300ms -count=5
tinygo test -run '^$' -bench '^BenchmarkDecode$' \
  -benchmem -benchtime=300ms -count=5 ./compress/zstd
```

Set `GOMAXPROCS=1` and run comparisons sequentially. Use `tinygo test -opt=2`
for the speed-optimized decoder. TinyGo HTTP/WebSocket tests on this machine
also need the SDK paths described in the Base64 README.

[The original 500 raw samples](internal/bytealg/testdata/bench-20261020.txt) are saved in the
repository. Before/after test binaries and machine-readable summaries from this
run remain in `/tmp/tinygodriver-zstd-opt-20261020/` as temporary artifacts.
