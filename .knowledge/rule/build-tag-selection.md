---
id: rule:build-tag-selection
type: rule
title: Build Tag Selection Convention
---
Backend selection follows the repository convention already used by `compress/zstd`, `httpmux`, and `database/sql/sqlite`.

```yaml
tags:
  std_path: "//go:build !tinygo && !force_tinygo_logic"
  native_path: "//go:build (tinygo || force_tinygo_logic) && <goos>"
  force_tinygo_logic:
    purpose: run the native backend under host go so it is testable without tinygo
    required_by: requirement:test-strategy
  darwinstarttlswith13:
    purpose: >
      replace both darwin backends with mbedTLS, buying TLS 1.3 on the upgrade
      path and client certificates, at the cost of OS trust policy
    decided_by: decision:darwin-hybrid-tls
per_backend_files:
  darwin_default: "(tinygo || force_tinygo_logic) && darwin && !darwinstarttlswith13"
  mbedtls: "(tinygo || force_tinygo_logic) && (linux || (darwin && darwinstarttlswith13))"
  linux: "(tinygo || force_tinygo_logic) && linux"
  windows: "(tinygo || force_tinygo_logic) && windows"
  fallback: "(tinygo || force_tinygo_logic) && !darwin && !linux && !windows"
  vendored_c: >
    the mbedTLS sources in rule:mbedtls-vendoring carry the linux native tag
    too, so a std-go build never compiles them
test_tags:
  rule: >
    a _test.go file gates the code it tests, so its constraint must match that
    package's, not a subset of it. A narrower tag compiles under some other
    configuration and silently proves nothing about the one it names
  observed: >
    https/mbedtls_test.go said `force_tinygo_logic && !tinygo && ...` while
    internal/mbedtls says `(tinygo || force_tinygo_logic) && ...`. The host half
    of that pair does not define MBEDTLS_TINYGO_NEON, so the vendored
    arm_neon.h the test claimed to gate was never compiled by any committed
    test. Fixed 2026-09-02 by widening the tag to the package's own
  check: >
    `tinygo test <pkg>` reporting "no tests to run" where a test is supposed to
    gate a tinygo-only path is this defect, not an empty package
rules:
  - exactly one dialTLS definition per build configuration
  - a catch-all file returns ErrPlatformNotSupported for unmatched GOOS
  - files importing crypto/tls carry "!tinygo", per requirement:no-crypto-tls-on-tinygo
  - cgo LDFLAGS stay in the per-OS file, never in a shared file
  - the public API is identical under every tag combination
precedent:
  - httpmux/mux_std.go
  - compress/zstd/writer_klauspost.go
  - fasthttp/zstd_tinygo.go, per decision:fasthttp-zstd-backend; the one place a third tag (fasthttp_nozstd) cuts across the pair
  - cloud/aws/transport_std.go, moved there by decision:aws-shared-package
redirect_policy_files: >
  the no-redirect helper splits on "!tinygo" rather than the std_path tag, because
  the tinygo code path runs under host go through force_tinygo_logic and needs the
  host http.Client told not to follow redirects; see
  requirement:s3-redirect-resigning. The helper is shared, the S3 re-signing
  behaviour is not; nosql/dynamodb has no redirects.
cgo_flag_files: >
  cgo flags live in per-OS, per-compiler files because tinygo and host go need
  different ones; see rule:tinygo-cgo-flag-limits
documented_in: README section "Implementation selection"
