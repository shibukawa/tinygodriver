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
additional testing with Clang. It does not select `archsimd`.

## Algorithm and compatibility

The bulk decoder validates 64 ASCII characters with four independent 16-byte
vectors, sharing one scalar validity check. Rejected batches retry the
single-vector path before committing any output. NEON uses the class bits
from validation with CLZ to select decoding offsets, avoiding a separate
punctuation correction. Its classifier tables are adapted from
[simdutf](https://github.com/simdutf/simdutf/blob/master/src/arm64/arm_base64.cpp);
see [NOTICE](NOTICE). Go's NEON backend packs bytes with table lookups and
per-byte shifts; x86 packs with 32-bit operations and a byte shuffle. The Go
bulk encoder expands 48 input bytes into 64 output characters using four
independent vectors.
TinyGo's NEON bulk decoder instead uses LD4 to separate each quantum's four
characters. Two immediate right shifts and three shift-inserts combine their
sextets, and ST3 writes exactly 48 decoded bytes. This removes eight TBL, eight
variable shifts and four OR operations from each 64-character block's packing
stage. Its bulk encoder uses LD3, a four-register alphabet lookup and ST4 to
encode exactly 48 input bytes into 64 characters. Clang's NEON builtins provide
these operations without an intrinsic header. The encoder retains its compact
12-byte loop on x86 and for ARM64 inputs below 256 raw bytes.

MIME's 76-character lines have a separate fast path: five vectors validate
the complete line, decode its 57 bytes and consume LF, CR/LF or CR together.
The twelve-character fringe uses an exact nine-byte store. Other CR/LF between
complete quanta and fringes of 4, 8 or 12 valid characters use the single-vector
path. TinyGo selects the MIME loop before entering its plain bulk loop, so
plain input pays no MIME lookahead per block. After a rejected bulk block, an
independent small loop processes the remaining input. Short or irregular lines
then avoid repeated bulk validation and bulk-loop bookkeeping.
A quantum split by whitespace, final padding and errors go through the
standard library, preserving decoded prefixes and absolute `CorruptInputError`
offsets. Partial vector stores preserve in-place decoding; no load or store
crosses a slice's length, including when spare capacity exists.

The Go encoder keeps its packer within the compiler's four-field SSA limit
and holds constants outside the loop. Inspection of the arm64 instructions
confirmed that the previous five vector reloads from the stack per iteration
are gone. Fixed-size array views of already-checked slices also remove
redundant bounds checks and pointer adjustment for each vector in a batch.
The direct Array load/store intrinsics and combined translate/pack/store
expressions also avoid NOPs that Go 1.27 inserts for unmatched inline markers.
Disassembly on arm64 shows 21 → 4 NOPs per plain decode iteration, 25 → 8 per
MIME iteration, and 8 → 0 per bulk encode iteration. The remaining host gap
includes an intrinsic API limitation: `archsimd` exposes neither structured
loads/stores nor shift-insert, so its kernel still uses more table lookups.
TinyGo explicitly inlines the packing helpers: its default size optimization
otherwise outlines them and inserts a function call for every vector. Its
fallback keeps the previous 16-byte loop and constants in a separate function
so bulk-kernel register allocation does not penalize short or irregular lines.
Inputs shorter than 256 encoded bytes go directly to that compact SIMD loop
to avoid setting up both decoders for one small message. Its 3-, 6- and 9-byte
fringes use constant-size copies. With `-opt=z` this eliminates a `memcpy` call
and reduces the compact decoder's stack frame from 304 to 16 bytes. Complete
vectors also use fixed 16-to-12 strides, leaving variable digit bookkeeping
only on the fringe path. The full-vector loop falls from 46 to 36 instructions
in the inspected `-opt=z` binary.

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
CC=clang go test -tags=base64_c ./encoding/base64
tinygo test ./encoding/base64
clang -O2 -g -fsanitize=address,undefined \
  encoding/base64/testdata/vector_bounds.c -o /tmp/base64-bounds
/tmp/base64-bounds
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
dispatch thresholds at all 16 alignments. `TestEncodeKernelBufferEdges` also
checks incomplete source/output blocks at those alignments, preserving source
bytes and destination canaries. The standalone sanitizer harness allocates
exact object lengths to check source reads as well as destination writes.
`TestMixedLineWidths` exercises transitions between MIME, plain and compact
loops, including leading separators and malformed suffixes.
The fuzz target uses the standard library as
its oracle and seeds both LF and CR/LF MIME input.

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
LLVM 22.1.4. The baseline is the previous bulk implementation at `9f7123a`.
Baseline and updated binaries ran sequentially with the same benchmark
functions. Each row uses 64 KiB of binary data. Speeds are decimal GB/s,
baseline → updated, with zero allocations per operation:

| Operation | Go, `GOEXPERIMENT=simd` | TinyGo, default `-opt=z` | TinyGo, `-opt=2` |
|---|---:|---:|---:|
| Encode | 13.12 → 14.56 | 15.33 → 21.93 | 16.00 → 21.71 |
| Decode, no newlines | 10.05 → 11.04 | 11.18 → 12.99 | 11.66 → 12.39 |
| Decode, MIME 76/LF | 9.08 → 9.67 | 10.32 → 11.50 | 10.38 → 11.66 |

These are medians of five 300 ms measurements without CPU pinning. The
64-byte benchmark also checks dispatch overhead. TinyGo's new encoder
selection costs about 0.7 ns (`-opt=z`) or 1.1 ns (`-opt=2`) at that size;
encoding remains on the compact loop. Plain decoding improves from 2.41 to
2.49 GB/s with `-opt=z` and from 3.12 to 3.47 GB/s with `-opt=2`.

For a same-machine reference, Python 3.14.7 with fastpybase64 0.1.1,
simdutf 9.3.2 and its `arm64` kernel measured 19.80 GB/s encoding,
12.44 GB/s decoding plain data and 9.62 GB/s decoding MIME 76/LF. The Python
measurement uses the identical xorshift payload, five samples of at least
300 ms, and includes result allocation and interpreter-loop overhead. Both
benchmarks use one thread; their API costs differ. TinyGo is now comparable
to this reference for plain decoding and faster for encoding and MIME in
this run. General single-core CPU benchmark ratios do not predict Base64
throughput: SIMD width, instruction selection, validation and packing matter.

Reproduce the Python reference in an optional virtual environment:

```sh
python3 -m venv /tmp/tinygodriver-base64-reference
/tmp/tinygodriver-base64-reference/bin/python -m pip install fastpybase64==0.1.1
/tmp/tinygodriver-base64-reference/bin/python \
  encoding/base64/testdata/bench_fastpybase64.py
```

Additional line layouts also use 64 KiB binary payloads and medians of five
300 ms samples, baseline → updated in GB/s:

| Line layout | Go, `GOEXPERIMENT=simd` | TinyGo, default `-opt=z` | TinyGo, `-opt=2` |
|---|---:|---:|---:|
| 32 characters, LF | 5.80 → 5.82 | 7.93 → 9.67 | 7.90 → 7.97 |
| 64 characters, LF | 8.20 → 8.69 | 9.51 → 12.38 | 10.11 → 11.67 |
| 76 characters, CR/LF | 8.83 → 9.19 | 10.11 → 10.82 | 10.11 → 11.52 |

CPU scheduling and code layout affect absolute results; compare optimization
settings with your own workload. Short 100 ms samples showed large outliers
on this machine, so these line-layout comparisons use the longer samples above.
