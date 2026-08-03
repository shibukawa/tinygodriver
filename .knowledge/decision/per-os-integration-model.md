---
id: decision:per-os-integration-model
type: decision
title: Per-OS TLS Integration Model
---
Each OS backend uses the integration model its native TLS API is designed for; no C code or byte-plumbing strategy is shared across platforms.

```yaml
state: accepted
seam: api:tls-dialer
models:
  bio_callback:
    platforms: [linux]
    backend: system:mbedtls
    shape: >
      vendored mbedTLS compiled by tinygo's cgo; Go owns the socket and mbedTLS
      reaches it through mbedtls_ssl_set_bio callbacks
    socket_owner: go
    superseded: >
      the fd-attached system:openssl plan, which tinygo cannot link at all
  connection_owning:
    platforms: [darwin]
    backend: system:network-framework
    shape: C owns the whole connection; nw_connection_t performs DNS, TCP, and TLS
    socket_owner: c
    note: no Go-side socket exists, so system:tinygo-netdev is bypassed on this path
  buffer_transform:
    platforms: [windows]
    backend: system:schannel
    shape: SSPI called from C via cgo; Go owns the socket and moves the tokens
    socket_owner: go
    language: c
    revised: >
      this entry said "pure Go via syscall, the only backend without C". That
      was wrong. TinyGo ships no windows syscall package, so syscall.NewLazyDLL
      does not exist and SSPI has to be reached through cgo, as
      netdev/sys_windows.go already does for winsock. The mingw requirement was
      therefore never actually removed. See
      requirement:windows-tinygo-feasibility.
    consequence: >
      all three backends are now C, so the model split is about who owns the
      socket, not about which language calls the API.
    dividend: >
      because SSPI never sees the socket, windows is the only platform where
      api:tls-dialer's dial and upgrade paths are the same code. darwin needs
      two backends for that.
rejected:
  single_shared_model:
    reason: >
      forcing one model makes two of three backends fight their API. Schannel has
      no fd concept; Network.framework refuses to be a byte transformer.
  reuse_netdev_ipproto_tls:
    reason: >
      IPPROTO_TLS hides TLS below net.Conn, leaving no place to attach
      data:https-config per request
cost_accepted: three independent native implementations and three test paths
sibling_without_a_socket:
  what: api:rsa-signer, added by decision:native-rsa-signing
  why_it_is_not_in_the_table: >
    this taxonomy classifies backends by who owns the socket. A signer owns
    none: bytes in, bytes out, plus a key handle. It obeys
    rule:cgo-bridge-contract and the toolchain rules unchanged, and it needs no
    model of its own.
  consequence: >
    "per-OS" in this repository now covers more than TLS, so this concept's
    title is narrower than its subject matter
