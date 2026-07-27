---
id: decision:macos-network-framework
type: decision
title: macOS Uses Network.framework
---
Network.framework is the darwin TLS 1.3 backend, selected by the `darwintls13` build tag. It was the default until decision:macos-secure-transport.

```yaml
state: superseded_as_default
chosen: system:network-framework, now behind the darwintls13 build tag
superseded_by: decision:macos-secure-transport
why_superseded: >
  nw_connection owns DNS, TCP and TLS as one unit, so TLS cannot be started on
  a socket that has already carried plaintext. That rules out in-band upgrade
  protocols such as PostgreSQL and MySQL STARTTLS. It remains the only way to
  get TLS 1.3 on darwin, so it is kept as an opt-in.
rationale:
  - Apple's current, non-deprecated networking API
  - ships in libSystem; removes the Homebrew openssl@3 runtime dependency
  - system trust evaluation and hostname check come for free
rejected:
  keep_openssl:
    reason: >
      requires Homebrew openssl@3 at build and run time. A package manager
      dependency is unacceptable, so OpenSSL is excluded from darwin entirely.
    status: hard exclusion, not a tradeoff
accepted_costs:
  - nw_connection is async; needs blocking bridge in flow:tls-dial-tinygo
  - callbacks are Clang blocks, unverified under tinygo cgo
  - DNS and TCP move into C, so this path bypasses system:tinygo-netdev
gated_by: requirement:macos-blocks-poc
fallback_if_gate_fails:
  chosen: Secure Transport, SSLRead/SSLWrite in Security.framework
  reason: >
    also ships in the OS, so it preserves the no-package-manager constraint.
    Deprecated since 10.15 but still functional, and synchronous, so it needs
    no blocks and no dispatch bridge.
  not_an_option: OpenSSL, per the rejection above
