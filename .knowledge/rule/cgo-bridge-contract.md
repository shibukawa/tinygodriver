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
  not_windows: system:schannel is pure Go, so none of this applies there
detail: flow:tls-dial-tinygo
