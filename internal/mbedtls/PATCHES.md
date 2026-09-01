# Vendored mbedTLS

Upstream: mbedTLS **3.6.7** LTS, `mbedtls-3.6.7.tar.bz2`
sha256 `a7e8bcbec0e6f761b4af24f25677626b35f762f68eef79c08677a363212d11f6`

Reproduce with:

```bash
python3 internal/mbedtls/vendor.py /path/to/mbedtls-3.6.7.tar.bz2
```

Do not edit the vendored files by hand. Change `vendor.py` and re-run, so an
upgrade re-applies every transformation deliberately.

## Why vendored at all

TinyGo cannot link the OS OpenSSL. It emits no `PT_INTERP`, so dynamic
relocations are never applied and the first `libcrypto` call jumps to address
0; a static link instead needs roughly forty shim symbols whose exact set
depends on how the distribution compiled OpenSSL. Building mbedTLS from source
through TinyGo's own cgo avoids both, because the result is compiled against
TinyGo's musl and linked statically.

## Layout

Sources sit flat in this directory, and the public headers in `mbedtls/` and
`psa/` beneath it. cgo only compiles C from the Go package directory and puts
that directory on the include path automatically, so this layout needs no `-I`.
That matters because TinyGo does not expand `${SRCDIR}` in `#cgo` lines and
ignores `CGO_CFLAGS`.

## Transformations

| Change | Reason |
|---|---|
| `//go:build (tinygo \|\| force_tinygo_logic) && linux` prepended to every `.c` | Without a constraint a std-Go build tries to compile them with cgo disabled and fails |
| `net_sockets.c` dropped | Needs the BSD socket API, which TinyGo's musl does not provide. `tls_mbedtls.c` supplies BIO callbacks over raw syscalls instead |
| `timing.c` dropped | Unused, and pulls in the timing API |
| `common.h`: NEON include routed through `MBEDTLS_TINYGO_NEON` | `common.h` keys NEON off `__ARM_NEON`, a compiler predefine no mbedTLS option controls and that TinyGo rejects `-U` for. TinyGo has no `arm_neon.h` at all |

### Config changes

Disabled, each needing a platform facility this build does not have:
`MBEDTLS_NET_C`, `MBEDTLS_TIMING_C`, `MBEDTLS_FS_IO`,
`MBEDTLS_PSA_ITS_FILE_C`, `MBEDTLS_PSA_CRYPTO_STORAGE_C`,
`MBEDTLS_HAVE_TIME_DATE`.

Enabled: `MBEDTLS_SELF_TEST`,
`MBEDTLS_SHA256_USE_ARMV8_A_CRYPTO_IF_PRESENT`,
`MBEDTLS_SHA512_USE_A64_CRYPTO_IF_PRESENT`.

`MBEDTLS_AESNI_C` and `MBEDTLS_AESCE_C` are on by default upstream and stay on.
The software fallback is roughly 27x slower for AES-GCM, so never disable them
to work around a build problem.

## Hand-written files

`tinygo_arm_neon.h` is not from upstream. It declares only the NEON surface
mbedTLS uses, with the crypto operations written as inline assembly rather than
compiler builtins, so it survives a TinyGo LLVM upgrade. The real 3.2 MB header
cannot be vendored instead: it is written against one exact clang version's
`__builtin_neon_*` signatures, and clang 21's copy failed to compile under the
clang 20.1.1 that TinyGo 0.41 bundled. The bundle keeps moving — TinyGo 0.42 is
on LLVM 22.1.4 — and the header rode that upgrade with no change, which is the
whole point of writing it in inline assembly.

Its correctness is not assumed. `MBEDTLS_SELF_TEST` is enabled and
`https/mbedtls_test.go` runs the AES, GCM, SHA-256 and SHA-512 known-answer
vectors. **If you touch that header, those tests are the gate — but only a
TinyGo run is.** `MBEDTLS_TINYGO_NEON` is set by `cgoflags_tinygo.go` alone, so
a host build compiles the real `<arm_neon.h>` and `go test -tags
force_tinygo_logic` passes without ever reading this file. The runs that do
reach it:

```bash
tinygo test -tags darwinstarttlswith13 ./https   # darwin/arm64
tinygo test ./https                              # linux
```

## Config hazard

`MBEDTLS_HAVE_TIME_DATE` **must stay enabled**. Without it mbedTLS skips
`notBefore` and `notAfter` entirely and accepts an expired certificate with no
error. It was switched off during development on the assumption that it needed
a platform facility this build lacks; it does not, musl provides `time()`.

The expired-certificate test caught it. Never relax a config option to make a
build succeed without first checking what verification it turns off.

## Upgrade duty

This repository ships a TLS implementation, so tracking advisories is a
maintenance obligation, not optional. On any mbedTLS advisory affecting 3.6.x:

1. bump `MBEDTLS_VERSION` and `EXPECTED_SHA256` in `vendor.py`
2. re-run `vendor.py`; it fails loudly if an upstream change broke a patch
3. run the package tests, including the self tests, on linux/arm64 and linux/amd64
4. rebuild the TinyGo example

Track the 3.6 LTS line. 4.x is a PSA-only redesign, not a drop-in upgrade.

## License

mbedTLS is Apache-2.0; see `LICENSE` in this directory.
