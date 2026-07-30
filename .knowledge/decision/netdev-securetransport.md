---
id: decision:netdev-securetransport
type: decision
title: netdev Uses Secure Transport on darwin
---
netdev's `IPPROTO_TLS` moves from OpenSSL to Secure Transport on darwin, removing the Homebrew dependency from every macOS binary in the repository.

```yaml
state: accepted
accepted_on: 2026-07-28
problem: >
  decision:darwin-hybrid-tls made the https darwin backends take their socket
  from system:tinygo-netdev, and netdev's darwin IPPROTO_TLS linked Homebrew
  openssl@3. Importing netdev therefore pulled openssl@3 into every macOS
  binary, defeating the no-package-manager property those backends were chosen
  for.
solution:
  backend: Secure Transport, shared through internal/securetransport
  why_it_fits: >
    the seam is sysTLSConnect(fd, hostname) plus send, recv and close, so it
    hands over an already connected descriptor. Secure Transport is a byte
    transformer with caller-supplied I/O and adopts one directly. An
    nw_connection cannot: it owns DNS, TCP and TLS together. Secure Transport
    is therefore the only OS-provided option here, the same reasoning that put
    it on the upgrade path in decision:darwin-hybrid-tls.
  sharing: >
    the C bridge moved to internal/securetransport so netdev and https compile
    one copy. It is not gated behind force_tinygo_logic, because netdev uses it
    on plain host-Go darwin builds too.
verified_2026_07_28:
  linkage: >
    otool on both darwin tinygo builds lists only Security, CoreFoundation and
    Network; no /opt/homebrew entries remain
  tests: netdev IPPROTO_TLS passes, including its SSL_CERT_FILE case
cost:
  tls_version: >
    TLS 1.2 for net.Dial("tls") on darwin, where OpenSSL negotiated 1.3
  deprecation: Apple marks Secure Transport deprecated; it still ships and works
behaviour_preserved:
  ssl_cert_file: >
    netdev has always honored it. Secure Transport verifies against the
    keychain and knows nothing of the variable, so the file is read in Go and
    passed in as an extra trust anchor.
error_reporting: >
    the OpenSSL implementation collapsed every failure into four generic
    strings. The replacement names the failure class and carries the OSStatus.
still_openssl: >
  nothing. host-Go linux was the last holdout and moved to crypto/tls in
  decision:netdev-crypto-tls-on-linux.
