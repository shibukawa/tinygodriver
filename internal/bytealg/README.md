# Byte kernels for protocol parsers

`Mask`, `IndexByte` and `ValidHeaderValue` provide bounded fast paths for the
repository's WebSocket, XML and HTTP implementations. They allocate nothing.

| Build | Backend |
|---|---|
| Go, `GOEXPERIMENT=simd`, arm64 | `simd/archsimd`, NEON |
| Go, `GOEXPERIMENT=simd`, amd64 with AVX2 | `simd/archsimd`, runtime CPU check |
| TinyGo with cgo, darwin/linux/windows arm64 | Clang vectors, NEON |
| TinyGo with cgo, darwin/linux/windows amd64 | Clang vectors, baseline SSE2 |
| Other builds | Scalar masking/validation and standard-library byte search |

Standard Go always uses `bytes.IndexByte`, retaining its existing architecture
optimizations. TinyGo uses the bounded C vector search on inputs of at least
64 bytes. Masking and header validation also select vectors at 64 bytes.
The HTTP parser retains its inline scalar table loop for shorter values.

`Mask` consumes a multiple-of-16 prefix with a repeating little-endian 32-bit
key. Callers keep their existing scalar tail and key-position handling. Both
WebSocket forks use this kernel. `ValidHeaderValue` preserves HTAB, visible
ASCII and obs-text, rejecting other controls and DEL. XML uses `IndexByte`
without changing refill offsets or token boundaries.

All loads and stores stay inside the slice's length, including unaligned
inputs. C uses fixed-size `memcpy` operations for unaligned vector access and
requires no intrinsic headers or external library. The optional `bytealg_c`
tag selects this backend under host Go for validation with Clang.

```sh
go test ./internal/bytealg
GOEXPERIMENT=simd go test ./internal/bytealg
CC=clang go test -tags bytealg_c ./internal/bytealg
tinygo test ./internal/bytealg
clang -O2 -g -fsanitize=address,undefined \
  internal/bytealg/testdata/vector_bounds.c -o /tmp/bytealg-bounds
/tmp/bytealg-bounds
```

See [measurements](../../SIMD_BENCHMARKS.md) for workloads and measured gains.
