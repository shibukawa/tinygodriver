---
id: decision:netdev-crypto-tls-on-linux
type: decision
title: netdev IPPROTO_TLS Uses crypto/tls on Host-Go Linux
---
netdev's `IPPROTO_TLS` moves from OpenSSL to `crypto/tls` on linux, removing the last OpenSSL dependency in the repository.

```yaml
state: accepted
accepted_on: 2026-07-31
selection_axis: >
  tinygo versus !tinygo, which is the compiler. It is not force_tinygo_logic,
  the tag requirement:test-strategy uses to reach native code from host go.
  netdev picks its TLS backend by compiler alone.
what_was_wrong:
  tag: netdev/tls_openssl_linux.go was `linux && !tinygo`
  consequence: >
    unreachable from any tinygo binary, because the tinygo side of the seam is a
    stub that returns ErrProtocolNotSupported. The adapter existed only for
    standard-Go builds.
  cost: >
    its cgo LDFLAGS carried -lssl -lcrypto, so every standard-Go linux build
    importing system:tinygo-netdev linked libssl whether or not it ever used
    IPPROTO_TLS. An optional feature was an unconditional link-time dependency.
why_crypto_tls:
  - already linked into a standard-Go build, so nothing new is imported
  - x509.SystemCertPool honors SSL_CERT_FILE and SSL_CERT_DIR, which is what SSL_CTX_set_default_verify_paths gave, so trust behavior is unchanged
  - real error values instead of the four generic strings the OpenSSL adapter collapsed every failure into
  - no package manager on any platform, which is what requirement:os-native-tls wanted
why_not_on_darwin_or_windows: >
  there the TLS files carry a bare `darwin` or `windows` tag, so the same code
  runs in a tinygo binary. Replacing it with crypto/tls would delete the
  shipping implementation. Linux is the only platform whose host-Go backend is
  not also its tinygo backend, which is what makes the standard library correct
  here and wrong there.
implementation:
  file: netdev/tls_crypto_linux.go
  adapter: >
    a net.Conn over the raw descriptor built on netdev's own sysSend, sysRecv,
    waitRead and waitWrite. net.FileConn is unusable because it dups the
    descriptor onto the netpoller while Device.Send and Device.Recv still select
    on the original, and the dup shares the file description so non-blocking
    mode would leak back.
  ownership: >
    the adapter's Close is a no-op. Device.Close calls sysTLSClose and then
    closes the descriptor itself, so closing it in both places would be a double
    close.
  removed: netdev/tls_openssl_linux.go, netdev/tls_openssl.h, netdev/tls_errors.go
scope_note: >
  this changes no behavior any shipped binary depends on. register_std.go makes
  useNetdev a no-op, so standard Go never routes net.Dial through netdev, and
  api:https-transport never uses netdev's TLS seam on any platform. The gain is
  the removed dependency, not new capability.
does_not_affect: decision:linux-mbedtls, which is about what tinygo can link
verified: >
  every package builds and tests on linux/arm64 with no libssl-dev installed,
  including TestIPProtoTLS; darwin and its tinygo build unchanged
