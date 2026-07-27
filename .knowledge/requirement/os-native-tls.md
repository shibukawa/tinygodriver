---
id: requirement:os-native-tls
type: requirement
title: OS-Native TLS Backends
---
TinyGo builds must perform TLS through the OS-provided stack, with no vendored or statically embedded TLS implementation.

```yaml
priority: must
backends:
  darwin: system:network-framework
  windows: system:schannel
  linux: system:mbedtls, vendored; this requirement does not hold there
acceptance:
  - no TLS protocol logic is implemented in this repository
  - protocol versions, cipher suites, and curves come from OS defaults
  - a security update to the OS TLS stack applies without rebuilding the app
  - darwin and windows need no package manager and no third-party library
  - linux cannot satisfy this requirement; see decision:linux-mbedtls
package_manager_ban:
  scope: darwin
  rule: no Homebrew, MacPorts, or vendored TLS on the darwin path
  consequence: >
    system:openssl is excluded from darwin; if system:network-framework fails
    its gate, the replacement is Secure Transport, never OpenSSL
rationale: >
  a pure-Go or vendored TLS stack would exceed tinygo binary and RAM budgets
  and shift CVE response onto this repository
exception:
  platform: linux
  status: requirement relaxed; decision:linux-mbedtls accepted 2026-07-26
  reason: >
    tinygo cannot link the OS OpenSSL at all, so the choice is a vendored
    system:mbedtls or no linux support. The CVE-tracking cost this requirement
    exists to avoid is accepted on linux only.
selected_via: decision:per-os-integration-model
