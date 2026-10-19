# encoding/base64

Base64 encoding and decoding with the `encoding/base64` API, accelerated with
separate SIMD backends for Go and TinyGo. Standard, URL, padded and unpadded
encodings use the same fast path. Change the import to use it:

```go
import "github.com/shibukawa/tinygodriver/encoding/base64"

encoded := base64.RawURLEncoding.EncodeToString(payload)
decoded, err := base64.RawURLEncoding.DecodeString(encoded)
```

For reusable output buffers, use `Encode`, `Decode`, `AppendEncode` or
`AppendDecode`. `DecodeString` allocates its output and borrows the immutable
input string without copying it. No decoder writes to its source.

## Selecting the implementation

| Build | Backend |
|---|---|
| Go, `GOEXPERIMENT=simd`, arm64 | `simd/archsimd`, 128-bit NEON |
| Go, `GOEXPERIMENT=simd`, amd64 with AVX2 | `simd/archsimd`, 128-bit vectors; runtime CPU check |
| Go without the experiment, other architectures, or amd64 without AVX2 | Standard library |
| TinyGo with cgo, darwin/linux/windows arm64 | Clang vectors and NEON |
| TinyGo with cgo, darwin/linux/windows amd64 with SSSE3 | Clang vectors and SSSE3; runtime CPUID check |
| Other TinyGo targets, cgo disabled, or amd64 without SSSE3 | Standard library |

```sh
GOEXPERIMENT=simd go build ./your/app
tinygo build -opt=2 -o app ./your/app
```

TinyGo does not implement Go's `archsimd` compiler intrinsics. With TinyGo
0.42.0 and Go 1.27.0, a direct `archsimd.BroadcastUint8x16(...).StoreArray(...)`
probe fails to link the intrinsic symbols, even with `GOEXPERIMENT=simd`.
The TinyGo backend therefore uses C vector types. Small inline assembly
wrappers provide table lookup, byte shifts, leading-zero count and x86 mask
extraction where needed; it requires neither an external library nor
compiler-version-specific intrinsic headers. The normal TinyGo build selects
this backend automatically.

The optional `base64_c` build tag selects that same C backend with host Go for
additional testing. It does not select `archsimd`.

## Algorithm and compatibility

The bulk decoder validates 64 ASCII characters with four independent 16-byte
vectors, sharing one scalar validity check. Rejected batches retry the
single-vector path before committing any output. NEON uses the class bits
from validation with CLZ to select decoding offsets, avoiding a separate
punctuation correction. Its classifier tables are adapted from
[simdutf](https://github.com/simdutf/simdutf/blob/master/src/arm64/arm_base64.cpp);
see [NOTICE](NOTICE). NEON packs bytes with table lookups and per-byte shifts;
x86 packs with 32-bit operations and a byte shuffle. The Go bulk encoder expands
48 input bytes into 64 output characters using four independent vectors.
TinyGo's encoder processes 12 input bytes per iteration.

MIME's 76-character lines have a separate fast path: five vectors validate
the complete line, decode its 57 bytes and consume LF, CR/LF or CR together.
The twelve-character fringe uses an exact nine-byte store. Other CR/LF between
complete quanta and fringes of 4, 8 or 12 valid characters use the single-vector
path. After a rejected bulk block, an independent small loop processes the
remaining input. Short or irregular lines then avoid repeated bulk validation
and bulk-loop bookkeeping.
A quantum split by whitespace, final padding and errors go through the
standard library, preserving decoded prefixes and absolute `CorruptInputError`
offsets. Partial vector stores preserve in-place decoding; no load or store
crosses a slice's length, including when spare capacity exists.

The Go encoder keeps its packer within the compiler's four-field SSA limit
and holds constants outside the loop. Inspection of the arm64 instructions
confirmed that the previous five vector reloads from the stack per iteration
are gone. Fixed-size array views of already-checked slices also remove
redundant bounds checks and pointer adjustment for each vector in a batch.
TinyGo explicitly inlines the packing helpers: its default size optimization
otherwise outlines them and inserts a function call for every vector. Its
fallback keeps the previous 16-byte loop and constants in a separate function
so bulk-kernel register allocation does not penalize short or irregular lines.
Inputs shorter than 256 encoded bytes go directly to that compact SIMD loop
to avoid setting up both decoders for one small message.

`WithPadding`, `Strict`, custom alphabets and zero-value encodings retain
standard-library semantics. Custom alphabets and small inputs use scalar code.
`NewEncoder` and `NewDecoder` use the standard streaming adapters; their
internal chunk processing is not accelerated. `Encoding` is this package's
own concrete type, so code that explicitly exchanges `*encoding/base64.Encoding`
with another package must adapt that boundary.

The repository's JWT, Datastore, PostgreSQL SCRAM, HTTP and WebSocket helpers
use this package. Mbed TLS's internal PEM codec is separate and unchanged.

## Verification and benchmarks

```sh
go test ./encoding/base64
GOEXPERIMENT=simd go test ./encoding/base64
go test -tags=base64_c ./encoding/base64
tinygo test ./encoding/base64
GOEXPERIMENT=simd go test ./encoding/base64 -run '^$' \
  -fuzz FuzzDecodeCompatibility -fuzztime=10s -parallel=2

GOEXPERIMENT=simd go test ./encoding/base64 -run '^$' \
  -bench 'BenchmarkBase64/65536/' -benchtime=300ms -count=5
tinygo test -opt=2 -run '^$' \
  -bench 'BenchmarkBase64/65536/' -benchtime=300ms -count=5 ./encoding/base64
GOEXPERIMENT=simd go test ./encoding/base64 -run '^$' \
  -bench '^BenchmarkBase64LineBreaks$' -benchtime=100ms -count=3
```

Tests compare output, partial output, error types and error positions against
the standard library, including every byte value in every vector lane, CR/LF
inside quanta, padding, strict trailing bits, exact-length buffers, canaries,
in-place decoding, append methods and streaming adapters. `TestVectorKernels`
also checks the selected SIMD kernel directly when a vector backend is available.
`TestBatchDecodeCompatibility` puts invalid bytes and CR/LF in all 80 positions
of a later batch, including the MIME fringe and next-line lookahead.
`TestBatchBufferEdges` checks input/output lengths around the batch and C
dispatch thresholds at all 16 alignments. The fuzz target uses the standard library as its oracle
and seeds both LF and CR/LF MIME input.

The native tests ran on darwin/arm64 with both compilers, including TinyGo's
default `-opt=z` and `-opt=2`. Go and TinyGo also passed the WASI scalar path.
The x86 C kernels ran under Rosetta. The Go amd64 SIMD backend was
cross-compiled and its CPU fallback ran under Rosetta; that environment did
not expose AVX2, so native execution of the Go AVX2 kernel is unverified.

The affected Go callers passed their regression suites. TinyGo passed JWT,
WebSocket and fasthttp suites, plus Datastore's value-codec tests. On this
machine the HTTP callers needed SDK 26.5 search paths: TinyGo's bundled linker
cannot parse SDK 27's `arm64e.x1` TAPI entries. Base64 and JWT tests need no SDK
override. For the integration tests only, the compatible installed SDK was
selected without changing system settings:

```sh
base64_test_sdk=/Library/Developer/CommandLineTools/SDKs/MacOSX26.5.sdk
tinygo test -ldflags="-extldflags '-F${base64_test_sdk}/System/Library/Frameworks -L${base64_test_sdk}/usr/lib -L${base64_test_sdk}/usr/lib/system'" \
  ./websocket ./fasthttpwebsocket ./fasthttp
```

The benchmarks include each compiler's standard library and optimized
backend on the same data. They reuse destination buffers, use one processing
loop, and report decoded/binary bytes per second for both operations. MIME
input uses LF after every 76 encoded characters. Allocating Python APIs in
[Daniel Lemire's article](https://lemire.me/blog/2026/10/17/decoding-base64-in-python-at-10-gigabytes-per-second/)
are a different measurement condition.

Measured on 2026-10-20, Apple M3, darwin/arm64, Go 1.27.0, TinyGo 0.42.0 /
LLVM 22.1.4. The table compares the previous SIMD implementation (main at
`21e676e`, which merged `7a9f2dd`) with this bulk implementation. Both versions
were rebuilt and measured sequentially on this machine with the same benchmark
functions, including the new line-break benchmark copied into the parent
checkout. Each row uses 64 KiB of binary data. Speeds are decimal GB/s,
previous SIMD → bulk SIMD, with zero
allocations per operation:

| Operation | Go, `GOEXPERIMENT=simd` | TinyGo, default `-opt=z` | TinyGo, `-opt=2` |
|---|---:|---:|---:|
| Encode | 9.62 → 13.72 | 16.14 → 16.01 | 16.05 → 16.05 |
| Decode, no newlines | 8.67 → 10.26 | 9.14 → 11.61 | 9.54 → 11.86 |
| Decode, MIME 76/LF | 4.83 → 9.17 | 6.42 → 10.41 | 6.18 → 10.48 |

These are medians of five 300 ms measurements without CPU pinning. A second
size, 64 bytes, checks dispatch overhead: Go encoding was 4.54 → 5.88 GB/s;
TinyGo's default decoder without newlines was 2.56 → 2.42 GB/s (25.0 → 26.5 ns).
The C dispatch thus still adds a small cost to short-message decoding, despite
using the compact kernel. TinyGo's encoder retains its 12-byte loop.

Additional line layouts use 64 KiB of binary data and medians of three 100 ms
measurements, again previous SIMD → bulk SIMD in GB/s:

| Line layout | Go, `GOEXPERIMENT=simd` | TinyGo, default `-opt=z` | TinyGo, `-opt=2` |
|---|---:|---:|---:|
| 32 characters, LF | 5.57 → 5.87 | 7.88 → 8.05 | 8.09 → 8.09 |
| 64 characters, LF | 6.90 → 8.29 | 8.60 → 10.22 | 8.78 → 10.23 |
| 76 characters, CR/LF | 4.63 → 8.97 | 5.90 → 10.22 | 5.75 → 10.33 |

CPU scheduling and code layout affect absolute results; compare optimization
settings with your own workload.
