---
id: vision:tinygo-https
type: vision
title: TinyGo HTTPS Client Package
---
Give TinyGo desktop builds a working HTTPS client with a `net/http`-shaped API and no `crypto/tls`, using the host OS TLS stack where TinyGo can reach it and a vendored one where it cannot.

```yaml
problem:
  - TinyGo crypto/tls is a stub; net/http client cannot reach https URLs
  - netdev IPPROTO_TLS covers darwin only and needs Homebrew openssl@3
  - linux tinygo and windows return ErrProtocolNotSupported
goal:
  - one import provides Get/Post/PostForm/Head over TLS on tinygo and std go
  - same source compiles and behaves under both compilers
  - no package manager and no runtime library install on any target
non_goal:
  - server-side TLS in v1
  - replacing system:tinygo-netdev for plain TCP
tls_source_per_platform:
  darwin: OS, via system:network-framework
  windows: OS, via system:schannel
  linux: >
    vendored system:mbedtls, because tinygo cannot link the OS OpenSSL at all.
    This is a deliberate exception, recorded in decision:linux-mbedtls.
  std_go: crypto/tls
delivers:
  - api:https-functions
  - api:https-transport
constrained_by:
  - decision:v1-scope
  - decision:per-os-integration-model
  - requirement:platform-matrix
