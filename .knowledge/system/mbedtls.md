---
id: system:mbedtls
type: system
title: mbedTLS Backend
---
Linux TLS backend, vendored as source and compiled by TinyGo's own cgo, which removes both failure modes found in requirement:linux-tinygo-openssl-poc.

```yaml
platform: linux
version_verified: 3.6.7 LTS
model: bio_callback
availability: vendored source; no runtime package required on the target
why_it_works:
  - compiled by tinygo's clang against tinygo's musl, so there is no libc mismatch
  - links statically, so the missing PT_INTERP never matters
verified_2026_07_26:
  arches: [linux/arm64, linux/amd64]
  stages_passed:
    - library links and reports its version
    - entropy and CTR-DRBG seed from the getrandom syscall
    - TLS handshake plus HTTP GET against a local server with a custom CA
    - untrusted certificate rejected, MBEDTLS_ERR_X509_CERT_VERIFY_FAILED -0x2700
    - hostname mismatch rejected
    - skip-verify connects
  binary: static, "not a dynamic executable", about 3.5 MB
integration:
  sockets:
    problem: mbedTLS net_sockets.c needs the BSD socket API tinygo's musl lacks
    solution: drop net_sockets.c and use mbedtls_ssl_set_bio with our own callbacks
    benefit: matches the fd-from-Go shape the real backend needs anyway
  entropy:
    solution: custom entropy source calling getrandom directly
    reason: avoids mbedTLS platform entropy, which wants /dev/urandom through stdio
  trust_store:
    note: >
      mbedTLS has no system trust store. The Go side must read
      /etc/ssl/certs/ca-certificates.crt and pass PEM in, which data:https-config
      already expresses.
build_adjustments:
  disabled_config: [MBEDTLS_NET_C, MBEDTLS_TIMING_C, MBEDTLS_FS_IO, MBEDTLS_PSA_ITS_FILE_C, MBEDTLS_PSA_CRYPTO_STORAGE_C]
  enabled_config: [MBEDTLS_SELF_TEST, MBEDTLS_AESNI_C, MBEDTLS_AESCE_C]
  removed_sources: [net_sockets.c, timing.c]
  hardware_acceleration: rule:mbedtls-hw-acceleration
  shim_symbols: [inet_pton]
  shim_note: one symbol, versus about 40 for static OpenSSL
  srcdir_note: >
    tinygo does not expand ${SRCDIR} in #cgo lines, so include paths cannot be
    written relative to the package. See rule:tinygo-cgo-flag-limits.
tradeoff: requirement:os-native-tls no longer holds on linux; see decision:linux-mbedtls
