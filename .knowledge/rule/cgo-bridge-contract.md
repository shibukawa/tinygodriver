---
id: rule:cgo-bridge-contract
type: rule
title: Go/C Bridge Contract
---
Rules every native backend follows so TinyGo's runtime and GC stay safe.

```yaml
rules:
  memory:
    - C never retains a Go pointer past the call that received it
    - buffers crossing the boundary are C.malloc'd or are pinned slices used only during the call
    - every native handle is a uintptr opaque to Go, freed by exactly one close path
  callbacks:
    - C must not call into Go; results are written into a struct the Go side reads after the call
    - reason: tinygo goroutines are not OS threads and a foreign thread cannot enter the scheduler
  errors:
    - every C entry point returns int, zero for success and a stable negative code otherwise
    - out-parameters are written only on success
    - codes map to sentinels per requirement:error-classification
  threading:
    - a native connection handle is used by one goroutine at a time
    - shared setup, such as mbedTLS entropy seeding, happens once under sync.Once
  strings:
    - Go strings are NUL-terminated into a byte slice before crossing
precedent: netdev/tls_openssl.h follows the int-return and uintptr-handle shape
applies_to:
  darwin: system:network-framework, where blocks run on libdispatch threads
  linux: system:mbedtls, where the BIO callbacks run on the calling thread
  windows: >
    system:schannel. This entry used to read "not_windows: schannel is pure Go",
    which was wrong: tinygo ships no windows syscall package, so that backend is
    cgo like the other two. It calls no Go from C and keeps every native handle
    behind a uintptr, so it satisfies this contract unchanged.
header_exception:
  platform: windows
  rule: >
    the windows bridge includes the system headers rather than redeclaring
    them, unlike darwin, which cannot reach framework headers under tinygo.
    SSPI and crypt32 layouts are too intricate to hand-declare safely on a
    platform this repository cannot run.
detail: flow:tls-dial-tinygo
