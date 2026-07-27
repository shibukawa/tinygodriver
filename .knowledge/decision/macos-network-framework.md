---
id: decision:macos-network-framework
type: decision
title: macOS Uses Network.framework
---
The darwin backend targets Network.framework (`nw_connection_t`) instead of OpenSSL or Secure Transport.

```yaml
state: accepted
chosen: system:network-framework
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
