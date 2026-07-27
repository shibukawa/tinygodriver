---
id: decision:darwin-hybrid-tls
type: decision
title: macOS Hybrid TLS, with an mbedTLS Opt-In
---
darwin carries Network.framework and Secure Transport together, picking per operation; `darwinstarttlswith13` swaps both for mbedTLS.

```yaml
state: accepted
accepted_on: 2026-07-28
supersedes:
  - decision:macos-secure-transport
  - decision:macos-network-framework
  - the darwintls13 build tag, now removed
default_build:
  dial: Network.framework, TLS 1.3
  upgrade: Secure Transport, TLS 1.2
  rationale: >
    nw_connection owns DNS, TCP and TLS as one unit, so it reaches 1.3 but
    cannot adopt a socket that already carried plaintext. Secure Transport is
    a byte transformer with caller-supplied I/O, so it can, but has no 1.3.
    Neither covers both needs, so the build carries both.
  verified: both link into one binary under host go and tinygo
opt_in_build:
  tag: darwinstarttlswith13
  backend: system:mbedtls, the same vendored copy linux uses
  gain: TLS 1.3 on both paths, client certificates, one code path with linux
  cost:
    size: about 1.2 MB to 1.7 MB
    trust_policy: >
      the real cost. The default backends hand verification to the OS, so admin
      and MDM distrust settings and Apple's evaluation policy apply. mbedTLS
      validates against /etc/ssl/cert.pem, a static snapshot; a root an
      administrator distrusted is still in that file. Linux had no OS policy to
      give up, which is why the same trade was easy there and is not here.
version_asymmetry:
  rule: >
    WithMinVersion(VersionTLS13) on the upgrade path returns ErrProtocolVersion
    rather than negotiating something weaker
  reason: the asymmetry must be visible, not incidental
regression_introduced:
  detail: >
    the darwin backends now take their socket from system:tinygo-netdev, whose
    darwin IPPROTO_TLS path uses OpenSSL, so both darwin builds link Homebrew
    openssl@3. That defeats the no-package-manager property these backends were
    chosen for.
  fix: make netdev's OpenSSL path opt-in; it is a change to that package
  status: open, documented in the package README
verified_2026_07_28:
  - 20 tests on the hybrid, 19 on darwinstarttlswith13, 16 on std go
  - tinygo end-to-end passes on both darwin builds and on linux
