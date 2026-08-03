---
id: rule:mbedtls-vendoring
type: rule
title: mbedTLS Vendoring and Update Duty
---
How the mbedTLS sources live in the repository, and the security obligation that comes with shipping them.

```yaml
layout:
  path: internal/mbedtls/
  contents: mbedTLS library/*.c and *.h, plus include/, flattened as cgo requires
  reason: >
    cgo only compiles C sources that sit in the package directory, and
    rule:tinygo-cgo-flag-limits notes tinygo does not expand ${SRCDIR}, so
    include paths must be resolvable without it
  excluded_sources:
    net_sockets.c: needs the BSD socket API tinygo's musl lacks; BIO callbacks replace it
    timing.c: unused, and pulls in the timing API
local_patches:
  file: common.h
  change: >
    gate the <arm_neon.h> include so the bundled minimal header can be selected
  rule: >
    every local patch is recorded in a PATCHES file with its reason, so an
    upgrade can re-apply or drop it deliberately
bundled_header:
  file: tinygo_arm_neon.h
  reason: see rule:mbedtls-hw-acceleration
config_hazard:
  option: MBEDTLS_HAVE_TIME_DATE
  rule: must stay enabled
  why: >
    disabling it makes mbedTLS skip notBefore and notAfter entirely, so an
    expired certificate is accepted with no error. It was switched off during
    development on the assumption that it needed a platform facility this build
    lacks; it does not, musl provides time().
  caught_by: >
    requirement:test-strategy's expired-certificate case, which is the reason
    that case exists. Never relax a config option to make a build succeed
    without checking what verification it turns off.
update_duty:
  trigger: any mbedTLS security advisory affecting 3.6.x
  steps:
    - bump to the new 3.6 LTS patch release
    - re-apply the recorded patches
    - re-run requirement:test-strategy, including the known-answer self tests
    - rebuild on linux/arm64 and linux/amd64
  note: >
    this duty exists only because decision:linux-mbedtls relaxed
    requirement:os-native-tls for linux
version_pinning:
  current: 3.6.7
  track: the 3.6 LTS line, not 4.x, which is a PSA-only redesign
license:
  mbedtls: Apache-2.0, compatible with this repository
  attribution: keep the upstream LICENSE file inside the vendored directory
