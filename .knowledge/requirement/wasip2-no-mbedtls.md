---
id: requirement:wasip2-no-mbedtls
type: requirement
title: wasip2 Build Without mbedtls
---
A `-target=wasip2` build fails at `internal/mbedtls` with `fatal: 'stdlib.h' file not found` because rule:tinygo-wasip2-goos makes the `linux` constraint match while the wasi sysroot carries no C headers; the requester calls wasip2 the realistic server-side WASM target since WASI preview 2 has wasi-sockets, so this blocks the WASM path entirely.

```yaml
priority: high
requested_by: system:popcornwave, 2026-08-07, against v1.1.9
symptom: internal/mbedtls mbedtls.go and sign.go fail cgo preprocessing, no stdlib.h
root_cause: >
  the tag (tinygo || force_tinygo_logic) && (linux || ...) admits the cgo files
  because wasip2 reports GOOS=linux; see rule:tinygo-wasip2-goos
asks_any_of:
  - >
    exclude mbedtls under the wasip2 tag so the build completes without TLS,
    failing explicitly per requirement:platform-matrix
  - >
    delegate TLS to the host through wasi-tls once that seam is practical,
    the same shape requirement:os-native-tls uses on real OSes
non_goal: porting mbedtls onto the wasi sysroot
same_exclusion_needed: netdev/sys_linux.go, admitted on wasip2 by the same GOOS
shipped_2026_08_07:
  how: >
    every linux-tagged constraint that wasip2 wrongly matched now carries
    !wasip2: internal/mbedtls (cgo files and cgoflags), netdev sys_linux.go and
    tls_linux_tinygo.go, https dial/trust/upgrade/proxy mbedtls-and-native
    files, and internal/rsasign key_linux.go. The unsupported counterparts
    widened to cover wasip2, so mbedtls reports Supported=false and https
    returns ErrPlatformNotSupported.
  chose: >
    the explicit-failure option; wasi-tls host delegation stays open as the
    follow-up that would make wasip2 a real TLS platform
  verified: >
    tinygo -target=wasip2 builds netdev+https+sqlite+pgx/stdlib and runs under
    wasmtime (component model), dial returns ErrPlatformNotSupported; needed
    brew-installed wasm-tools for TinyGo's component embed step
```
