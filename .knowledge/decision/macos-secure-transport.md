---
id: decision:macos-secure-transport
type: decision
title: macOS Defaults to Secure Transport
---
The darwin default backend is Secure Transport; Network.framework moves behind the `darwintls13` build tag.

```yaml
state: accepted
accepted_on: 2026-07-28
default: Secure Transport, via SSLSetIOFuncs
opt_in: Network.framework under -tags darwintls13
rationale:
  in_band_upgrade: >
    Secure Transport is a byte transformer with caller-supplied I/O, so TLS can
    start on a socket that has already carried plaintext. That is what
    PostgreSQL and MySQL STARTTLS need. decision:macos-network-framework
    structurally cannot do it, because nw_connection owns DNS, TCP and TLS
    together.
  simpler_binding: >
    SSLSetIOFuncs takes plain C function pointers, so this backend needs no
    Clang blocks, no libdispatch, and none of the -fblocks and -lsystem_blocks
    plumbing in rule:tinygo-darwin-toolchain
  uniformity: >
    Go owns the socket, the same shape as system:mbedtls on linux, so both
    native backends now share one model
cost:
  tls_version:
    detail: Secure Transport stops at TLS 1.2; kTLSProtocol12 is its highest constant
    handling: >
      WithMinVersion(VersionTLS13) fails with ErrProtocolVersion rather than
      quietly negotiating something weaker
    escape_hatch: -tags darwintls13
  deprecation: >
    Apple marks Secure Transport "No longer supported. Use Network.framework."
    It still ships and works; the darwintls13 tag is the hedge if that changes.
still_unsupported:
  client_certificates: >
    unchanged. Both darwin backends need a keychain-resident SecIdentityRef,
    so requirement:tls-client-config still records darwin as refusing them.
verified_2026_07_28:
  - 16 package tests pass on the default and on the darwintls13 build
  - tinygo end-to-end passes on both
implementation: api:tls-dialer
