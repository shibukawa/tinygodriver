# encoding/hex

Standard-library-compatible hexadecimal encoding and decoding with separate
SIMD backends for Go and TinyGo. Change the import:

```go
import "github.com/shibukawa/tinygodriver/encoding/hex"

encoded := hex.EncodeToString(payload)
decoded, err := hex.DecodeString(encoded)
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
| Go, `GOEXPERIMENT=simd`, arm64 | `simd/archsimd`, NEON |
| Go, `GOEXPERIMENT=simd`, amd64 with AVX2 | `simd/archsimd`, runtime CPU check |
| TinyGo with cgo, darwin/linux/windows arm64 | Clang vectors, NEON |
| TinyGo with cgo, darwin/linux/windows amd64 | Clang vectors, baseline SSE2 |
| Other builds | Standard library |

```sh
GOEXPERIMENT=simd go build ./your/app
tinygo build -opt=2 -o app ./your/app
```

SIMD starts at 32 raw bytes for encoding or 64 hex characters for decoding.
The encoder reads 16 bytes and writes exactly 32 characters per step. The
decoder validates all 32 input characters before writing their 16 decoded
bytes; rejected batches and fringes return to the standard decoder. Short
destinations also go through the standard implementation. No vector read or
write crosses the slice's length, including unaligned buffers.

Go's NEON encoder uses alphabet lookups and interleaved stores; its decoder
packs even/odd nibbles. Go's amd64 encoder widens bytes and maps nibbles
arithmetically. The C kernel uses Clang vectors without intrinsic headers,
assembly libraries or instruction sets beyond SSE2 on amd64. The optional
`hex_c` tag selects this C kernel under host Go for additional validation.

The repository's AWS signing, S3 hashing, zstd ETag and vendored PostgreSQL
helpers use this package. PostgreSQL bytea text encoding appends directly into
the caller's buffer with `AppendEncode`, avoiding an intermediate hex string.

## Verification

```sh
go test ./encoding/hex
GOEXPERIMENT=simd go test ./encoding/hex
CC=clang go test -tags hex_c ./encoding/hex
tinygo test ./encoding/hex
tinygo test -opt=2 ./encoding/hex
clang -O2 -g -fsanitize=address,undefined \
  encoding/hex/testdata/vector_bounds.c -o /tmp/hex-bounds
/tmp/hex-bounds
GOEXPERIMENT=simd go test ./encoding/hex -run '^$' \
  -fuzz '^FuzzCompatibility$' -fuzztime=15s -parallel=2
```

Tests compare every byte value in all 128 positions of a later vector batch,
partial output, error identity, odd-length input, exact-length buffers,
canaries, in-place decoding, append APIs, and streaming adapters. The C harness
uses exact allocations under AddressSanitizer and UndefinedBehaviorSanitizer.
The PostgreSQL bytea regression test checks prefixes, nil versus empty values,
the valuer interface and valuer error propagation.

Native arm64 tests passed under Go, Go SIMD, Clang, and TinyGo at both
optimization settings. amd64 Go/C test binaries also ran under Rosetta;
this does not establish native amd64 performance. Go and TinyGo WASI fallback
builds passed. Go SIMD and C fuzz runs completed 871,356 and 1,474,394 executions.

AWS/S3/zstd caller tests passed under Go, Go SIMD and TinyGo. PostgreSQL package
tests and the vendored bytea test passed under Go; the bytea test also passed
under TinyGo. PostgreSQL server integration requires a separately configured
server and was not part of this run.

## Measurements

Measured on Apple M3, Go 1.27.0 and TinyGo 0.42.0 / LLVM 22.1.4 on
2026-10-20. Medians of five 300 ms samples, one benchmark at a time with
`GOMAXPROCS=1`. Each cell is standard → selected decimal GB/s for 64 KiB:

| Operation | Go SIMD | TinyGo `-opt=z` | TinyGo `-opt=2` |
|---|---:|---:|---:|
| Encode | 1.73 → 17.64 (10.18×) | 1.85 → 24.35 (13.20×) | 1.86 → 24.31 (13.06×) |
| Decode | 1.54 → 7.74 (5.04×) | 1.50 → 9.55 (6.39×) | 1.24 → 9.56 (7.74×) |

At 32 raw bytes, encoding improves 4.40× under Go SIMD, 2.77× under default
TinyGo and 5.39× with `-opt=2`. Sixteen-byte inputs retain scalar handling and
can pay up to about 1.7 ns extra under TinyGo's default size optimization.
Every benchmark sample reports 0 B/op and 0 allocs/op after setup. These are
in-memory conversion measurements, not complete application throughput.

See the [combined measurements](../../SIMD_BENCHMARKS.md) and the raw samples
in `testdata/bench-20261020.txt`. The benchmark reuses destination buffers and
compares each compiler's standard library with this package in the same binary.
Throughput counts raw binary bytes for both encoding and decoding.

```sh
GOMAXPROCS=1 GOEXPERIMENT=simd go test ./encoding/hex -run '^$' \
  -bench '^BenchmarkHex$' -benchmem -benchtime=300ms -count=5
GOMAXPROCS=1 tinygo test -run '^$' -bench '^BenchmarkHex$' \
  -benchmem -benchtime=300ms -count=5 ./encoding/hex
```
