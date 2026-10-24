# encoding/simdhex

Standard-library-compatible hexadecimal encoding and decoding with separate
SIMD backends for Go and TinyGo. The package is named `simdhex` so that it
can sit next to the standard `encoding/hex` without an alias; code that
already says `hex.` can alias the import instead. Change the import:

```go
import "github.com/shibukawa/tinygodriver/encoding/simdhex"

encoded := simdhex.EncodeToString(payload)
decoded, err := simdhex.DecodeString(encoded)
```

`Encode`, `Decode`, `AppendEncode`, `AppendDecode`, `EncodeToString` and
`DecodeString` use the fast path. Encoding emits lowercase; decoding accepts
both letter cases. `InvalidByteError` and `ErrLength` retain standard-library
identity, including partial output and the invalid-character precedence for
odd-length input. In-place decoding is supported.

`NewEncoder`, `NewDecoder`, `Dump` and `Dumper` delegate to the standard
library. Their internal streaming/formatting loops are not SIMD accelerated.
`DecodeString` borrows its immutable input without a byte-slice copy; all
decode kernels read their source without modifying it.

## Backend selection

| Build | Backend |
|---|---|
| Go, arm64 | Go assembly, NEON; no experiment needed |
| Go, `GOEXPERIMENT=simd`, amd64 with AVX2 | `simd/archsimd`, runtime CPU check |
| Go with the `purego` tag on arm64, amd64 without the experiment or AVX2, other architectures | Standard library |
| TinyGo with cgo, darwin/linux/windows arm64 | Clang vectors, NEON |
| TinyGo with cgo, darwin/linux/windows amd64 | Clang vectors, baseline SSE2 |
| Other TinyGo targets, or cgo disabled | Standard library |

```sh
go build ./your/app
GOEXPERIMENT=simd go build ./your/app
tinygo build -opt=2 -o app ./your/app
```

The first command is enough on arm64; the experiment only matters on amd64.

SIMD starts at 32 raw bytes for encoding or 64 hex characters for decoding.
The encoder reads 16 bytes and writes exactly 32 characters per step. The
decoder validates all 32 input characters before writing their 16 decoded
bytes; rejected batches and fringes return to the standard decoder. Short
destinations also go through the standard implementation. No vector read or
write crosses the slice's length, including unaligned buffers.

Go's arm64 assembly encoder looks both nibbles up in the alphabet with TBL
and lets ST2 interleave them into the output, 32 characters per store. Its
decoder loads 32 characters with LD2, which separates the high-nibble and
low-nibble characters into two vectors, and maps every character through a
64-entry TBL table indexed by the character minus `'0'`: digits and letters of
either case become their value plus 16, everything else becomes zero. One
UMINV over both vectors rejects the block if any lane is zero, and SLI merges
the nibbles; the marker bit shifts out of the high nibble and lies outside the
four inserted bits of the low one. Go's amd64 `archsimd` encoder widens bytes
and maps nibbles arithmetically; its decoder packs even/odd nibbles. The C
kernel uses Clang vectors without intrinsic headers, assembly libraries or
instruction sets beyond SSE2 on amd64. The optional `hex_c` tag selects this
C kernel under host Go for additional validation.

Go on arm64 uses assembly because `archsimd` exposes neither LD2/ST2 nor
four-register TBL nor shift-insert, and because the experiment is off in
ordinary builds, which left every default `go build` with the standard
library. Go 1.27's assembler provides every needed instruction, so no
hand-encoded instruction words are required. The kernels use no stack frame
and need no `GOEXPERIMENT`; the `purego` tag selects the standard library
instead. A two-block unrolled decoder and an LD2/ST4 encoder were measured and
rejected: the first gained 2% at 64 KiB and lost 7% at 64 bytes, the second
encoded a third slower.

The repository's AWS signing, S3 hashing, zstd ETag and vendored PostgreSQL
helpers use this package. PostgreSQL bytea text encoding appends directly into
the caller's buffer with `AppendEncode`, avoiding an intermediate hex string.

## Verification

```sh
go test ./encoding/simdhex
go test -tags purego ./encoding/simdhex
GOEXPERIMENT=simd go test ./encoding/simdhex
CC=clang go test -tags hex_c ./encoding/simdhex
tinygo test ./encoding/simdhex
tinygo test -opt=2 ./encoding/simdhex
tinygo test -target=wasip1 ./encoding/simdhex
clang -O2 -g -fsanitize=address,undefined \
  encoding/simdhex/testdata/vector_bounds.c -o /tmp/hex-bounds
/tmp/hex-bounds
go test ./encoding/simdhex -run '^$' \
  -fuzz '^FuzzCompatibility$' -fuzztime=15s -parallel=2
```

Tests compare every byte value in all 128 positions of a later vector batch,
partial output, error identity, odd-length input, exact-length buffers,
canaries, in-place decoding, append APIs, and streaming adapters.
`TestVectorKernels` checks the selected kernel's contract directly: it
consumes whole blocks bounded by both slice lengths, stops before the first
block containing an invalid byte at any of 96 positions, accepts uppercase,
and leaves canaries beyond its reported output untouched. The C harness
uses exact allocations under AddressSanitizer and UndefinedBehaviorSanitizer.
The PostgreSQL bytea regression test checks prefixes, nil versus empty values,
the valuer interface and valuer error propagation.

Native arm64 tests passed on 2026-10-25 under Go's assembly backend in
default and `GOEXPERIMENT=simd` builds and with `-race`, under `purego`, under
Clang (`hex_c`) and under TinyGo at both optimization settings. The assembly
also passed natively on linux/arm64 in a container; windows/arm64, riscv64
and wasm were vetted and compiled, and the TinyGo WASI scalar path ran. The
assembly fuzz run completed 2,390,953 executions in 15 s; the 2026-10-20 Go
SIMD and C runs completed 871,356 and 1,474,394. The amd64 `archsimd`,
fallback and C test binaries ran under Rosetta, which offers AVX2 but not
AVX-512; this does not establish native amd64 performance. Before 2026-10-25
the `archsimd` decoder faulted there: its byte packing used
`SaturateToUint8`, which is VPMOVUSWB, an AVX-512 instruction behind the AVX2
guard. It now packs with VPMADDUBSW and VPSHUFB, every `archsimd` method the
amd64 files use is documented as AVX or AVX2, and the Rosetta fuzz run passed.

AWS, S3, zstd and vendored PostgreSQL package tests passed under Go with the
assembly backend. Their TinyGo runs date from 2026-10-20; the TinyGo backend
and its selection are unchanged. PostgreSQL server integration requires a
separately configured server and was not part of this run.

## Measurements

Measured on Apple M3, Go 1.27.0 and TinyGo 0.42.0 / LLVM 22.1.4 on
2026-10-25. Medians of five 300 ms samples, one benchmark at a time with
`GOMAXPROCS=1`, the four test binaries alternating in rotated order. Each cell
is standard → selected decimal GB/s for 64 KiB. The Go assembly column is a
default build; the previous `archsimd` column is commit `a1262d1` built with
`GOEXPERIMENT=simd`, the backend this assembly replaces.

| Operation | Go assembly | Previous Go `archsimd` | TinyGo `-opt=z` | TinyGo `-opt=2` |
|---|---:|---:|---:|---:|
| Encode | 1.86 → 36.12 (19.40×) | 1.86 → 19.77 (10.64×) | 1.84 → 24.15 (13.14×) | 1.86 → 24.23 (13.06×) |
| Decode | 1.67 → 16.17 (9.68×) | 1.67 → 8.44 (5.06×) | 1.48 → 9.47 (6.38×) | 1.24 → 9.48 (7.64×) |

32-byte binary input (the size of SHA-256 digests), ns/op:

| Operation | Go assembly | Previous Go `archsimd` | TinyGo `-opt=z` | TinyGo `-opt=2` |
|---|---:|---:|---:|---:|
| Encode | 20.16 → 4.03 (5.00×) | 20.19 → 4.60 (4.39×) | 19.61 → 7.52 (2.61×) | 15.97 → 2.96 (5.40×) |
| Decode | 20.97 → 5.91 (3.55×) | 21.07 → 7.27 (2.90×) | 23.38 → 9.59 (2.44×) | 26.79 → 4.30 (6.23×) |

The assembly encodes 1.8× and decodes 1.9× faster than the `archsimd` backend
and now leads the TinyGo C kernel; at 1 KiB it reaches 31.3 against 23.7 GB/s
encoding and 16.0 against 9.4 GB/s decoding. Sixteen-byte inputs retain
scalar handling. Go's dispatch costs under 1 ns there; TinyGo's `-opt=z`
build pays a few nanoseconds on decoding (2 ns on 2026-10-20, 6 ns in this
run, with the same kernel) and `-opt=2` nothing measurable. Every benchmark
sample reports 0 B/op and 0 allocs/op after setup. These are in-memory
conversion measurements, not complete application throughput.

See the [combined measurements](../../SIMD_BENCHMARKS.md) and the raw samples
in `testdata/bench-20261025.txt`; `testdata/bench-20261020.txt` holds the
earlier `archsimd` run. The benchmark reuses destination buffers and compares
each compiler's standard library with this package in the same binary.
Throughput counts raw binary bytes for both encoding and decoding.

```sh
GOMAXPROCS=1 go test ./encoding/simdhex -run '^$' \
  -bench '^BenchmarkHex$' -benchmem -benchtime=300ms -count=5
GOMAXPROCS=1 tinygo test -run '^$' -bench '^BenchmarkHex$' \
  -benchmem -benchtime=300ms -count=5 ./encoding/simdhex
```
