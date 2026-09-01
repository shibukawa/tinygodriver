---
id: rule:mbedtls-hw-acceleration
type: rule
title: mbedTLS AES Hardware Acceleration
---
The AES crypto extensions must stay enabled on both architectures; the software fallback is over twenty times slower and would dominate TLS bulk transfer cost.

```yaml
measured_2026_07_26:
  host: arm64 native, tinygo 0.41.1 static binary, 64 MiB workloads
  aes_256_gcm:
    software: 54 MiB/s
    hardware: 1455 MiB/s
    speedup: 27x
  sha_256:
    software: 362 MiB/s
    hardware: 2286 MiB/s
    speedup: 6.3x
  cpu_features_reported: AES, SHA2, and SHA512 all present via AT_HWCAP
rules:
  - keep MBEDTLS_AESNI_C and MBEDTLS_AESCE_C enabled; never ship software-only
  - keep MBEDTLS_SHA256_USE_ARMV8_A_CRYPTO_IF_PRESENT and
    MBEDTLS_SHA512_USE_A64_CRYPTO_IF_PRESENT enabled on arm64
  - keep MBEDTLS_SELF_TEST enabled and run the AES, GCM, SHA-256 and SHA-512
    known-answer vectors under `tinygo test`; they are what validate the
    vendored arm_neon.h, and only a tinygo build selects it
  - CPU support is detected at runtime, so a machine without the extensions
    still works through the software path
x86_64:
  status: intrinsics available with no extra work
  reason: >
    tinygo's clang resource dir already ships immintrin.h, wmmintrin.h and
    cpuid.h, so aesni.c compiles and detects support unchanged
arm64:
  status: needs a vendored arm_neon.h
  problem: >
    arm_neon.h is generated during an LLVM build rather than checked into the
    clang source tree, so tinygo's trimmed resource dir omits it. It also
    cannot be copied from another clang: the header is written against one
    exact version's __builtin_neon_* signatures, and clang 21's copy failed to
    compile under the clang 20.1.1 tinygo 0.41 bundled. The bundled compiler
    moves with every tinygo release; 0.42 is on LLVM 22.1.4.
  solution: >
    ship a minimal arm_neon.h covering only what library/aesce.c uses, with the
    crypto operations written as inline assembly rather than compiler builtins
  why_inline_asm: >
    instruction mnemonics are architectural and stable, so the header keeps
    working when tinygo upgrades LLVM, unlike a builtin-based copy
  surface:
    aes: [aese, aesd, aesmc, aesimc, pmull, pmull2, rbit]
    sha256: [sha256h, sha256h2, sha256su0, sha256su1, rev32]
    sha512: [sha512h, sha512h2, sha512su0, sha512su1, rev64]
    other: loads, stores, add, xor, dup, ext, shift, lane extraction, reinterprets
  sha512_note: >
    sha512.c ships inline-asm fallbacks for these four intrinsics, but they are
    gated on __clang_major__ < 13, so every clang tinygo has bundled needs them
    from the header
  runtime_detection: >
    aesce.c calls getauxval(AT_HWCAP), and tinygo's musl provides both
    sys/auxv.h and getauxval.c, so detection works unmodified
  validation: >
    correctness is not assumed. MBEDTLS_SELF_TEST runs the AES and GCM NIST
    known-answer vectors against this header, and they pass.
  validation_only_under_tinygo:
    what: >
      MBEDTLS_TINYGO_NEON is set by cgoflags_tinygo.go alone, so a host build
      compiles the real <arm_neon.h>. `go test -tags force_tinygo_logic` passes
      the vectors without ever reading the vendored header
    history: >
      until 2026-09-02 the only caller, https/mbedtls_test.go, carried
      `force_tinygo_logic && !tinygo`, so nothing committed compiled the header
      the docs called gated. Its tag now matches internal/mbedtls itself
    gate: >
      `tinygo test -tags darwinstarttlswith13 ./https` on darwin,
      `tinygo test ./https` on linux
x86_64_validation:
  status: correctness verified, throughput not
  verified:
    - aesni.c builds unchanged and AES-NI is detected at runtime
    - AES, GCM, SHA-256 and SHA-512 known-answer vectors all pass
  not_verified:
    throughput: >
      the run was under emulation, where AES-GCM measured 244 MiB/s and SHA-256
      267 MiB/s. Those numbers describe the emulator, not native x86, and are
      not comparable to the arm64 figures above.
  caveat: >
    emulated amd64 builds need the machine to themselves; two concurrent
    containers stalled the build for hours
  sha_on_x86: >
    mbedTLS 3.6 accelerates SHA only on aarch64. x86 gets AES-NI and CLMUL for
    AES-GCM, while SHA-256 stays software there.
  action: add a native linux/amd64 CI job to get real throughput numbers
