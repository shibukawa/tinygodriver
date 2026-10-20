# Zstd decoding measurements — 2026-10-20

This is the baseline recorded before the decoder optimizations. See the
[follow-up measurements](../../SIMD_BENCHMARKS.md) for the updated implementation.

The main optimization candidates are entropy decoding and bit-buffer refill.
In the Go profile of the TinyGo-compatible decoder, Huffman stream decoding
accounts for 70.2% of sampled CPU time on the level-1 fixture. Sequence decoding
accounts for 67.5–75.5% on the small-block and level-19 fixtures, including its
bit-buffer operations and output copies. Match copying accounts for 4.1–11.8%,
and XXH64 updates for 2.4–3.0%.

Native TinyGo `-opt=2` is already 1.20–1.41 times as fast as its default
`-opt=z` for `DecodeAll` here. No production decoding code was changed.

## Conditions

- Apple M3, darwin/arm64; Go 1.27.0; TinyGo 0.42.0 / LLVM 22.1.4.
- Three checked-in reference zstd 1.5.7 frames of the same 57,705-byte source
  document. Compressed input stays in memory; disk and network I/O are excluded.
- Every frame carries an XXH64 content checksum, verified during decoding.
- `DecodeAll` reuses its destination and pooled decoder; `Reader` uses `Reset`
  and `io.Copy(io.Discard, r)`. Each benchmark warms its buffers before timing.
  `DecodeAll` checks the expected length and SHA-256 before timing; `Reader`
  checks its warm-up output length. SHA-256 is not part of the timed work.
- Five 500 ms samples per condition, run sequentially with `GOMAXPROCS=1`.
  Each round rotates the four backends to reduce fixed ordering effects.
- No CPU pinning or isolation from other macOS workloads. Reported ranges are
  observed minima and maxima, not confidence intervals.

The four backends use the same public package API. `host` uses this package's
klauspost/compress v1.19.1 adapter (one decoder worker, low-memory mode);
`purego` compiles the TinyGo-compatible decoder with Go and
`-tags force_tinygo_logic`; the other two compile that decoder with TinyGo.
The host result is a comparison of implementations, not an isolated SIMD gain.

## Throughput

Decoded decimal MB/s. Cells show **median [minimum–maximum]** of five samples.

### DecodeAll

| Frame | Go / klauspost | Go / TinyGo logic | TinyGo `-opt=z` | TinyGo `-opt=2` |
|---|---:|---:|---:|---:|
| level6-1k-blocks | 527.0 [471.2–529.6] | 429.1 [424.8–437.6] | 267.8 [257.1–269.7] | 368.5 [364.0–372.5] |
| level19 | 593.2 [567.2–601.9] | 459.3 [397.0–473.3] | 270.3 [257.2–271.5] | 381.4 [381.0–383.8] |
| level1-stdin | 1033.5 [1005.5–1047.3] | 319.4 [221.9–320.5] | 288.6 [276.5–288.9] | 347.5 [345.0–349.5] |

### Reader

| Frame | Go / klauspost | Go / TinyGo logic | TinyGo `-opt=z` | TinyGo `-opt=2` |
|---|---:|---:|---:|---:|
| level6-1k-blocks | 527.8 [494.4–535.9] | 429.9 [421.4–436.5] | 266.3 [259.1–268.2] | 369.6 [360.4–372.6] |
| level19 | 598.1 [582.5–607.7] | 458.8 [456.5–465.5] | 266.9 [260.5–272.7] | 387.0 [381.6–392.0] |
| level1-stdin | 1032.8 [1011.9–1046.0] | 319.9 [272.4–321.1] | 285.0 [281.5–291.6] | 344.2 [329.3–351.4] |

All 120 throughput samples report **0 B/op and 0 allocs/op** after warm-up.
These numbers describe reuse, not first-use allocation or memory footprint.
`Reader` and `DecodeAll` have similar throughput for these in-memory inputs.
The workload is one small source document with three encodings; it does not
represent large archives, binary corpora, or dictionary-compressed frames.
Some Go samples varied substantially, especially the level-1 pure-Go fixture;
the full observed ranges are retained above rather than hidden by the median.

## CPU attribution

TinyGo 0.42.0's `runtime/pprof` is a stub, so these are **Go CPU profiles of
the TinyGo-compatible decoder**, collected separately from the throughput runs
with `-tags force_tinygo_logic`, `GOMAXPROCS=1` and `-benchtime=10s` per frame.
They identify candidates in the shared source, not exact native TinyGo costs.
Each profile contains approximately 10–12 CPU seconds of samples.

| Function / operation | Small blocks | Level 19 | Level 1 |
|---|---:|---:|---:|
| Huffman `decodeStream`, cumulative | 26.7% | 20.2% | 70.2% |
| `decodeSequences`, cumulative | 67.5% | 75.5% | 25.6% |
| `appendMatch`, cumulative | 9.5% | 11.8% | 4.1% |
| All `runtime.memmove`, flat | 12.7% | 15.0% | 4.5% |
| XXH64 `write`, cumulative | 3.0% | 2.9% | 2.4% |
| `backReader.fill`, flat | 25.3% | 22.7% | 40.8% |

**Rows overlap and must not be added.** `appendMatch` is inside
`decodeSequences`; `memmove` is called by match and literal copies;
`backReader.fill` is inside Huffman and sequence decoding. Cumulative includes
callees, while flat counts samples attributed to the function itself.

On level 1, the `decode4` call accounts for the same 70.2% as `decodeStream`:
its four independent streams currently run sequentially. Source attribution
places 4.48 CPU seconds in the stream loop's `br.fill()`, 1.91 seconds in the
Huffman table lookup, and 1.25 seconds in consumed-bit updates. Instruction
scheduling and sampling can shift line attribution, so these are approximate.

The first experiment should interleave the four Huffman streams, then compare
scalar instruction-level parallelism with explicit SIMD and measure native
TinyGo again. Small-block and level-19 improvements also need attention to
sequence bit extraction/refill. A copy-only or checksum-only SIMD change has
less room on this corpus. No SIMD speedup is established by this measurement.

## Reproduction

The decoder benchmark and embedded fixtures are shared across the host and
TinyGo builds; encoder-only benchmarks retain their original build tags.
Run these sequentially, without other CPU-heavy work:

```sh
GOMAXPROCS=1 go test ./compress/zstd -run '^$' \
  -bench '^BenchmarkDecode$' -benchmem -benchtime=500ms -count=5
GOMAXPROCS=1 go test -tags force_tinygo_logic ./compress/zstd -run '^$' \
  -bench '^BenchmarkDecode$' -benchmem -benchtime=500ms -count=5
GOMAXPROCS=1 tinygo test -opt=z -run '^$' \
  -bench '^BenchmarkDecode$' -benchmem -benchtime=500ms -count=5 ./compress/zstd
GOMAXPROCS=1 tinygo test -opt=2 -run '^$' \
  -bench '^BenchmarkDecode$' -benchmem -benchtime=500ms -count=5 ./compress/zstd
```

For the original round ordering, compile four binaries using `go test -c`,
`go test -tags force_tinygo_logic -c`, `tinygo test -opt=z -c`, and
`tinygo test -opt=2 -c`. Run each with `-test.run='^$'`,
`-test.bench='^BenchmarkDecode$'`, `-test.benchtime=500ms`, `-test.benchmem`,
and `-test.count=1`, rotating their starting order over five rounds.

Profile one fixture at a time:

```sh
GOMAXPROCS=1 go test -tags force_tinygo_logic ./compress/zstd -run '^$' \
  -bench '^BenchmarkDecode$/^DecodeAll$/^level1-stdin$' \
  -benchtime=10s -cpuprofile=/tmp/zstd-level1.pprof -o /tmp/zstd-profile.test
go tool pprof -top /tmp/zstd-profile.test /tmp/zstd-level1.pprof
go tool pprof -list=decodeStream /tmp/zstd-profile.test /tmp/zstd-level1.pprof
```

Replace `level1-stdin` with `level6-1k-blocks` or `level19` for the other profiles.

[Raw benchmark samples, profile summaries and source attribution](testdata/bench/decode-20261020.txt)
are saved alongside this report. Full binary profiles and compiled test programs from this run
are in `/tmp/tinygodriver-zstd-measure-20261020/` and are temporary artifacts.

Validation: all package tests passed under host Go, Go with
`force_tinygo_logic`, TinyGo `-opt=z`, and TinyGo `-opt=2`.
