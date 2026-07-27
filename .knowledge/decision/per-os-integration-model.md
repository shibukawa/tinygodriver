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
    shape: SSPI called from pure Go via syscall; no cgo, Go owns the socket
    socket_owner: go
    language: go, not c
    note: >
      the only backend without C. secur32.dll and crypt32.dll are reachable
      through syscall, which also removes the mingw toolchain requirement.
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
