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
| Go, arm64 | Go assembly, 128-bit NEON; no experiment needed |
| Go, `GOEXPERIMENT=simd`, amd64 with AVX2 | `simd/archsimd`, 128-bit vectors; runtime CPU check |
| Go with the `purego` tag on arm64, amd64 without the experiment or AVX2, other architectures | Standard library |
| TinyGo with cgo, darwin/linux/windows arm64 | Clang vectors and NEON |
| TinyGo with cgo, darwin/linux/windows amd64 with SSSE3 | Clang vectors and SSSE3; runtime CPUID check |
| Other TinyGo targets, cgo disabled, or amd64 without SSSE3 | Standard library |

```sh
go build ./your/app                     # arm64
GOEXPERIMENT=simd go build ./your/app   # amd64
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
additional testing with Clang. It selects neither the Go assembly nor
`archsimd`.

## Algorithm and compatibility

The bulk decoder validates 64 ASCII characters with four independent 16-byte
vectors, sharing one scalar validity check. Rejected batches retry the
single-vector path before committing any output. NEON uses the class bits
from validation with CLZ to select decoding offsets, avoiding a separate
punctuation correction. Its classifier tables are adapted from
[simdutf](https://github.com/simdutf/simdutf/blob/master/src/arm64/arm_base64.cpp);
see [NOTICE](NOTICE). x86 packs with 32-bit operations and a byte shuffle; the
Go amd64 bulk encoder expands 48 input bytes into 64 output characters using
four independent vectors.

Both NEON backends, Go assembly and TinyGo's C, share one algorithm. The bulk
decoder uses LD4 to separate each quantum's four characters. Two immediate
right shifts and three shift-inserts combine their sextets, and ST3 writes
exactly 48 decoded bytes. Compared with a table-lookup packer, this removes
eight TBL, eight variable shifts and four OR operations from each
64-character block. The bulk encoder uses LD3, a four-register alphabet lookup
and ST4 to encode exactly 48 input bytes into 64 characters. The Go assembly
then encodes at most one 24-byte block with the 8-byte forms of the same
instructions, and one 16-byte vector consuming 12 bytes. TinyGo keeps its
compact 12-byte loop on x86 and for ARM64 inputs below 256 raw bytes. Clang's
NEON builtins provide these operations without an intrinsic header.

MIME's 76-character lines have a separate fast path: five vectors validate the
complete line, decode its 57 bytes and consume LF, CR/LF or CR together. The
twelve-character fringe uses an exact nine-byte store. Other CR/LF between
complete quanta and fringes of 4, 8 or 12 valid characters use the
single-vector path. The NEON backends select the MIME loop before entering
their plain bulk loop, so plain input pays no MIME lookahead per block. After a
rejected bulk block, an independent small loop processes the remaining input.
Short or irregular lines then avoid repeated bulk validation and bulk-loop
bookkeeping. A quantum split by whitespace, final padding and errors go through
the standard library, preserving decoded prefixes and absolute
`CorruptInputError` offsets. Partial vector stores preserve in-place decoding;
no load or store crosses a slice's length, including when spare capacity
exists.

Go on arm64 uses assembly because `archsimd` exposes neither structured
loads/stores nor shift-insert; its kernel needed more table lookups and reached
only about 70% of the NEON C kernel's encoding speed. Go 1.27's assembler
provides every required instruction, including VLD3/VLD4, VST3/VST4,
four-register VTBL, VSLI, VCLZ and VUMINV, so no hand-encoded instruction words
are needed. The kernels use no stack frame and need no `GOEXPERIMENT`; the
`purego` tag selects the standard library instead. The small loop checks for
CR/LF only after a vector fails validation, keeping the separator test off the
common path. Like TinyGo, it sends inputs shorter than 256 encoded bytes
directly to that small loop. A two-block, 128-character decode loop was
measured and rejected: it was no faster, because the out-of-order core already
overlaps consecutive blocks.

The Go amd64 `archsimd` encoder keeps its packer within the compiler's
four-field SSA limit and holds constants outside the loop. Fixed-size array
views of already-checked slices remove redundant bounds checks and pointer
adjustment for each vector in a batch. The direct Array load/store intrinsics
and combined translate/pack/store expressions also avoid NOPs that Go 1.27
inserts for unmatched inline markers.

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
go test -tags=purego ./encoding/base64
GOEXPERIMENT=simd go test ./encoding/base64
CC=clang go test -tags=base64_c ./encoding/base64
tinygo test ./encoding/base64
clang -O2 -g -fsanitize=address,undefined \
  encoding/base64/testdata/vector_bounds.c -o /tmp/base64-bounds
/tmp/base64-bounds
go test ./encoding/base64 -run '^$' \
  -fuzz FuzzDecodeCompatibility -fuzztime=10s -parallel=2

go test ./encoding/base64 -run '^$' \
  -bench 'BenchmarkBase64/65536/' -benchtime=300ms -count=5
tinygo test -opt=2 -run '^$' \
  -bench 'BenchmarkBase64/65536/' -benchtime=300ms -count=5 ./encoding/base64
go test ./encoding/base64 -run '^$' \
  -bench '^BenchmarkBase64LineBreaks$' -benchtime=300ms -count=5
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
default `-opt=z` and `-opt=2`. Go's assembly backend passed in default and
`GOEXPERIMENT=simd` builds and under the fuzzer, and `purego` passed the scalar
path. The assembly also passed natively on linux/arm64 with Go 1.27.2 in an
OrbStack container; windows/arm64 was only compiled and vetted. Go and TinyGo
also passed the WASI scalar path.
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
LLVM 22.1.4. Each row uses 64 KiB of binary data. Speeds are decimal GB/s with
zero allocations per operation. The Go column compares the previous
`archsimd` backend (`9157612`, built with `GOEXPERIMENT=simd`) with the
assembly backend (default build); the two binaries ran alternately in five
rounds. The TinyGo backend is unchanged; its columns repeat the measurement
taken when its NEON kernel last changed (`8a8b983`).

| Operation | Go, `archsimd` → assembly | TinyGo, default `-opt=z` | TinyGo, `-opt=2` |
|---|---:|---:|---:|
| Encode | 14.63 → 21.49 | 21.93 | 21.71 |
| Decode, no newlines | 11.10 → 13.33 | 12.99 | 12.39 |
| Decode, MIME 76/LF | 9.68 → 11.70 | 11.50 | 11.66 |

These are medians of five 300 ms measurements without CPU pinning. At 1 KiB, Go
improves from 13.45 to 19.68 GB/s encoding, 9.59 to 11.33 GB/s decoding plain
data and 8.53 to 9.64 GB/s decoding MIME. The 64-byte benchmark also checks
dispatch overhead. There, Go encoding improves from 5.93 to 6.79 GB/s and plain
decoding from 3.20 to 3.37 GB/s. The 64-byte MIME input, one full line and a
short one, decodes slightly slower, 3.00 → 2.91 GB/s (about 0.7 ns): the
assembly loads all ten decoder table vectors for so little input, and the call
goes through Go's ABI0 assembly wrapper. TinyGo's encoder selection costs about
0.7 ns (`-opt=z`) or 1.1 ns (`-opt=2`) at that size; encoding remains on the
compact loop. Its plain decoding measured 2.49 GB/s with `-opt=z` and 3.47 GB/s
with `-opt=2`.

For a same-machine reference, Python 3.14.7 with fastpybase64 0.1.1, simdutf
9.3.2 and its `arm64` kernel measured 19.80 GB/s encoding, 12.44 GB/s decoding
plain data and 9.62 GB/s decoding MIME 76/LF. The Python measurement uses the
identical xorshift payload, five samples of at least 300 ms, and includes
result allocation and interpreter-loop overhead. Both benchmarks use one
thread; their API costs differ. Go's `archsimd` backend reached 74% of this
reference for encoding and 89% for plain decoding. Go's assembly backend and
TinyGo are now faster for encoding and MIME, and comparable or faster for plain
decoding. General single-core CPU benchmark ratios do not predict Base64
throughput: SIMD width, instruction selection, validation and packing matter.

Reproduce the Python reference in an optional virtual environment:

```sh
python3 -m venv /tmp/tinygodriver-base64-reference
/tmp/tinygodriver-base64-reference/bin/python -m pip install fastpybase64==0.1.1
/tmp/tinygodriver-base64-reference/bin/python \
  encoding/base64/testdata/bench_fastpybase64.py
```

Additional line layouts also use 64 KiB binary payloads and medians of five
300 ms samples in GB/s, with the same Go comparison and TinyGo measurement as
above:

| Line layout | Go, `archsimd` → assembly | TinyGo, default `-opt=z` | TinyGo, `-opt=2` |
|---|---:|---:|---:|
| 32 characters, LF | 5.89 → 7.43 | 9.67 | 7.97 |
| 64 characters, LF | 8.78 → 12.64 | 12.38 | 11.67 |
| 76 characters, CR/LF | 9.40 → 11.07 | 10.82 | 11.52 |

CPU scheduling and code layout affect absolute results; compare optimization
settings with your own workload. Short 100 ms samples showed large outliers
on this machine, so these line-layout comparisons use the longer samples above.
