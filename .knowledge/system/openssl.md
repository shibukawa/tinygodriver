---
id: system:openssl
type: system
title: OpenSSL 3 Backend
---
No longer used anywhere in the repository. Retained only because the failed integration attempts justify decision:linux-mbedtls.

```yaml
state: removed
removed_on: 2026-07-31
history:
  darwin: excluded to avoid Homebrew, per decision:macos-network-framework
  linux_https: replaced by system:mbedtls, per decision:linux-mbedtls
  linux_netdev: >
    the last user. netdev/tls_openssl_linux.go carried the tag
    `linux && !tinygo`, so it was reachable only from a host-Go build while the
    tinygo side of the seam refuses IPPROTO_TLS. It made libssl a dependency of
    every linux build in order to serve a path that never shipped, and was
    replaced by crypto/tls. See decision:netdev-crypto-tls-on-linux.
consequence: >
  no platform requires OpenSSL at build or run time, so libssl-dev is gone from
  the test environment described in requirement:test-strategy
why_retained: >
  requirement:linux-tinygo-openssl-poc is the evidence behind
  decision:linux-mbedtls, and that evidence is about OpenSSL specifically
past_gaps_that_kept_it_out_of_this_package:
  - no custom CA, client cert, or InsecureSkipVerify support in the netdev adapter
  - coarse error codes, insufficient for requirement:error-classification
  - a global mutex serialized every handshake
risk: requirement:linux-tinygo-openssl-poc
