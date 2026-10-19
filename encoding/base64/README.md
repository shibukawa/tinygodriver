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
The TinyGo backend therefore uses C vector types. Three small inline assembly
wrappers provide table lookup, byte shifts and the x86 mask extraction where
needed; it requires neither an external library nor compiler-version-specific
intrinsic headers. The normal TinyGo build selects this backend automatically.

The optional `base64_c` build tag selects that same C backend with host Go for
additional testing. It does not select `archsimd`.

## Algorithm and compatibility

The decoder validates 16 ASCII characters with two nibble lookup tables,
translates them to six-bit values and packs them into 12 output bytes. NEON
packs bytes with table lookups and per-byte shifts; x86 packs with 32-bit
operations and a byte shuffle. The encoder expands 12 input bytes into 16
six-bit values and translates them through a vector offset table.

CR and LF between complete quanta are skipped inside the SIMD loop. A line
fringe of 4, 8 or 12 valid characters is also processed there, so MIME's
76-character lines do not repeatedly exit and re-enter the backend. A quantum
split by whitespace, final padding and errors go through the standard library,
preserving decoded prefixes and absolute `CorruptInputError` offsets. Partial
vector stores preserve in-place decoding; no load or store crosses a slice's
length, including when spare capacity exists.

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
  -bench 'BenchmarkBase64/65536/' -benchtime=300ms -count=3
tinygo test -opt=2 ./encoding/base64 -run '^$' \
  -bench 'BenchmarkBase64/65536/' -benchtime=300ms -count=3
```

Tests compare output, partial output, error types and error positions against
the standard library, including every byte value in every vector lane, CR/LF
inside quanta, padding, strict trailing bits, exact-length buffers, canaries,
in-place decoding, append methods and streaming adapters. `TestVectorKernels`
also checks the selected SIMD kernel directly when a vector backend is available.
The fuzz target uses the standard library as its oracle.

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

The benchmarks compare each compiler's standard library with its optimized
backend on the same data. They reuse destination buffers, use one processing
loop, and report decoded/binary bytes per second for both operations. MIME
input uses LF after every 76 encoded characters. Allocating Python APIs in
[Daniel Lemire's article](https://lemire.me/blog/2026/10/17/decoding-base64-in-python-at-10-gigabytes-per-second/)
are a different measurement condition.

Measured on 2026-10-20, Apple M3, darwin/arm64, Go 1.27.0, TinyGo 0.42.0 /
LLVM 22.1.4. Each row uses 64 KiB of binary data. Speeds are decimal GB/s,
standard library → this package, with zero allocations per operation:

| Operation | Go, `GOEXPERIMENT=simd` | TinyGo, default `-opt=z` | TinyGo, `-opt=2` |
|---|---:|---:|---:|
| Encode | 3.03 → 10.28 | 2.95 → 16.01 | 2.91 → 16.13 |
| Decode, no newlines | 2.64 → 8.88 | 3.30 → 9.11 | 3.26 → 8.94 |
| Decode, MIME 76/LF | 1.96 → 5.04 | 2.47 → 6.41 | 2.57 → 5.03 |

These are medians of three measurements for Go and TinyGo `-opt=2`, and five
for TinyGo `-opt=z`, without CPU pinning. TinyGo's default was faster on MIME
decoding in this run; compare optimization settings with your own workload.
