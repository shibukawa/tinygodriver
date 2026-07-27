---
id: decision:linux-ktls-experiment
type: decision
title: kTLS Rejected for Binary Size
---
The kTLS prototype was removed because it does not reduce the TinyGo HTTPS binary materially.

```yaml
state: rejected
rejected_on: 2026-07-27
goal: reduce TinyGo Linux binary size
prototype:
  handshake_and_verification: system:mbedtls
  record_layer: Linux kTLS TLS_TX and TLS_RX
  scope: TLS 1.2 AES-128-GCM and AES-256-GCM
  build_verified:
    - linux arm64 host-go native backend
    - linux arm64 TinyGo linked client binary
  activation_probe: >-
    reached TCP_ULP after a verified handshake; the available OrbStack kernel
    lacked CONFIG_TLS, returned ENOENT, and exposed no /proc/net/tls_stat
size_measurement:
  target: TinyGo 0.41.1 linux arm64 examples/httpsclient
  regular_bytes:
    before: 6540504
    prototype: 6550576
    delta: 10072
  no_debug_bytes:
    before: 1889856
    prototype: 1892128
    delta: 2272
    percent: 0.12
reason: >-
  kTLS replaces only symmetric record processing. Handshake, certificate
  parsing, verification, trust handling via rule:linux-trust-store, and key
  establishment still require system:mbedtls, so the vendored TLS code remains.
outcome:
  - remove the prototype, WithKernelTLS API, tests, and documentation
  - retain decision:linux-mbedtls unchanged
  - reconsider kTLS only for measured CPU or throughput goals, not binary size
```
