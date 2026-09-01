---
id: rule:tinygo-cgo-flag-limits
type: rule
title: TinyGo cgo Flag and Header Limits
---
Cross-platform constraints on how TinyGo compiles cgo C code; every native backend in this repository is shaped by them.

```yaml
measured_on: tinygo 0.41.1, darwin/arm64 and linux/{arm64,amd64}; 0.42.0 adds the -fno-builtin-* row
flag_whitelist:
  accepted_cflags: [-I, -isystem, -isysroot, -D, -fblocks, -fno-builtin-*]
  rejected_cflags: [-F, -iframework, -U]
  accepted_ldflags: [-L, -l, -F, -framework]
  rejected_ldflags: bare file paths, such as passing a .tbd or .a directly
env_vars_ignored:
  vars: [CGO_CFLAGS, CGO_LDFLAGS]
  consequence: >
    every path must be literal in the #cgo line; users cannot override any of
    it from the environment
srcdir_not_expanded:
  detail: tinygo does not expand ${SRCDIR} in #cgo lines, unlike standard cgo
  consequence: >
    include paths cannot be written relative to the package, which matters for
    vendored C such as system:mbedtls
headers:
  nostdlibinc: tinygo compiles C with -nostdlibinc against a bundled libc
  trimmed_resource_dir: >
    the bundled clang resource directory omits arch intrinsics headers such as
    arm_neon.h, so any C that includes them must be patched or disabled
  consequence: >
    prefer hand-declaring the API surface, as netdev/tls_openssl.h and the
    darwin backend both do
predefined_macros:
  detail: >
    compiler predefines such as __ARM_NEON are set normally, so third-party C
    that keys optional code off them will try to include headers that are not
    reachable, and -U cannot switch them off
  workaround: patch the vendored source to honor a -D flag instead
builtin_suppression:
  detail: >
    -fno-builtin-<name> passes the whitelist, so an idiom LLVM synthesises into
    a libc call can be switched off one function at a time without -fno-builtin
  used_by: internal/mbedtls on darwin, for wcslen; see rule:tinygo-darwin-toolchain
linux_libc:
  detail: tinygo ships musl and its build omits the entire BSD socket API
  evidence: netdev/sys_linux.go already issues raw syscalls for this reason
  consequence: >
    C that calls socket, connect, poll, or inet_pton needs raw syscalls or a
    shim
linking:
  detail: tinygo linux emits no PT_INTERP, so dynamic relocations never apply
  consequence: link C dependencies statically; see requirement:linux-tinygo-openssl-poc
platform_specifics: rule:tinygo-darwin-toolchain
