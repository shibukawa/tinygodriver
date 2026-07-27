---
id: rule:tinygo-darwin-toolchain
type: rule
title: TinyGo darwin cgo Toolchain Constraints
---
TinyGo compiles cgo C files with `-nostdlibinc` against a bundled minimal macOS SDK and applies a flag whitelist, which dictates how the darwin backend must be written.

```yaml
general_limits: rule:tinygo-cgo-flag-limits
measured_on: tinygo 0.41.1, darwin/arm64
constraints:
  cflags_whitelist:
    rejected: [-F, -iframework]
    accepted: [-fblocks, -I, -isystem, -isysroot, -D]
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
    omits: all nw_* symbols and the blocks runtime
    consequence: -lsystem_blocks and -framework Network are both required
rules:
  - declare nw_*, sec_*, and dispatch_* by hand; include no system headers beyond stdlib
  - CFLAGS carries only -fblocks, so it stays portable
  - LDFLAGS lists both the Xcode and CommandLineTools SDK paths
  - "verified: the linker silently ignores a -L or -F path that does not exist"
  - blocks never call into Go; they write C memory and signal a semaphore
rationale: >
  hand-declaring also decouples the build from the SDK version, the same
  approach netdev/tls_openssl.h takes for OpenSSL
evidence: requirement:macos-blocks-poc
