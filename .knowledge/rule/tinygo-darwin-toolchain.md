---
id: rule:tinygo-darwin-toolchain
type: rule
title: TinyGo darwin cgo Toolchain Constraints
---
TinyGo compiles cgo C files with `-nostdlibinc` against a bundled minimal macOS SDK and applies a flag whitelist, which dictates how the darwin backend must be written.

```yaml
general_limits: rule:tinygo-cgo-flag-limits
measured_on: tinygo 0.41.1, darwin/arm64; re-measured on 0.42.0
constraints:
  cflags_whitelist:
    rejected: [-F, -iframework]
    accepted: [-fblocks, -I, -isystem, -isysroot, -D, -fno-builtin-*]
    consequence: framework headers are unreachable, so <Network/Network.h> cannot be included
  ldflags_whitelist:
    rejected: bare file paths such as a .tbd
    accepted: [-L, -l, -F, -framework]
    consequence: frameworks link only through -F plus -framework
  env_vars_ignored:
    detail: neither CGO_CFLAGS nor CGO_LDFLAGS reaches the compile or link step
    consequence: SDK paths must be literal in the #cgo line; users cannot override them
  minimal_sdk:
    provides: dispatch_*, os_retain, os_release, and about 3200 libSystem symbols
    omits: all nw_* symbols, the blocks runtime, and the wide-char string API
    consequence: -lsystem_blocks and -framework Network are both required
    wcslen_trap:
      what: >
        LLVM's loop-idiom pass rewrites a scan for the zero terminator of a
        four-byte-element array into a wcslen() call. The stub has no wcslen,
        so the link fails on C that never names the function
      seen: >
        tinygo 0.42 (LLVM 22) on internal/mbedtls ssl_tls.c:1185, walking a
        mbedtls_ecp_group_id array. tinygo 0.41 (LLVM 20) did not do this, so
        it arrived with the toolchain bump, not with a source change
      darwin_only: >
        tinygo's musl builds wcslen.c, so the same loop links on linux. Only
        the darwin libSystem stub is short the symbol
      fix: >
        -fno-builtin-wcslen in CFLAGS, which the whitelist accepts; see
        internal/mbedtls/cgoflags_darwin_tinygo.go
      reproduce: >
        fifteen lines of cgo: a size_t loop over a const enum* until the value
        is 0, built with tinygo for darwin
rules:
  - declare nw_*, sec_*, and dispatch_* by hand; include no system headers beyond stdlib
  - >
    after any tinygo upgrade, link the cgo-heavy packages before trusting the
    bump. A new LLVM can synthesise a libc call out of a loop that compiled
    fine before, and the stub is small enough that it will not resolve
  - CFLAGS carries only -fblocks, so it stays portable
  - LDFLAGS lists both the Xcode and CommandLineTools SDK paths
  - "verified: the linker silently ignores a -L or -F path that does not exist"
  - blocks never call into Go; they write C memory and signal a semaphore
rationale: >
  hand-declaring also decouples the build from the SDK version, the same
  approach netdev/tls_openssl.h takes for OpenSSL
evidence: requirement:macos-blocks-poc
