---
id: decision:linux-mbedtls
type: decision
title: Linux Uses Vendored mbedTLS
---
Linux abandons the OS TLS stack and vendors mbedTLS source, because both OpenSSL integration routes are blocked by TinyGo.

```yaml
state: accepted
accepted_on: 2026-07-26
approval_note: >
  the CVE-tracking cost was raised explicitly before work continued, and the
  user then directed further mbedTLS work, including hardware acceleration.
  That is treated as approval. Reopen if that reading is wrong.
chosen: system:mbedtls
version: 3.6.7 LTS
rejected:
  shared_openssl:
    reason: >
      tinygo emits no PT_INTERP, so relocations are never applied and the first
      libcrypto call jumps to address 0. Forcing an interpreter with -ldflags
      fixes that, but SSL_CTX_new then still fails at ssl_lib.c:4074.
    evidence: requirement:linux-tinygo-openssl-poc
  static_openssl:
    reason: >
      needs roughly 40 shim symbols whose exact set depends on how the distro
      compiled OpenSSL, so it would differ on every distro and release
consequences:
  positive:
    - verified end to end on linux/arm64 and linux/amd64
    - no runtime dependency; the target needs no libssl installed
    - fully static binary, so the PT_INTERP defect is irrelevant
    - one shim symbol instead of about forty
    - no -ldflags burden on the user
    - client certificates work here, unlike darwin
  negative:
    security_ownership: >
      this repository now ships a TLS implementation, so tracking mbedTLS
      advisories and bumping the vendored version is a maintenance duty.
      That is the cost requirement:os-native-tls was written to avoid.
      Governed by rule:mbedtls-vendoring.
    binary_size: about 3.8 MB, versus a 272 KB darwin binary
    trust_store: no system trust store; see rule:linux-trust-store
vendoring: rule:mbedtls-vendoring
acceleration: rule:mbedtls-hw-acceleration
