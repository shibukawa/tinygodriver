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
  darwintls13:
    purpose: >
      select Network.framework on darwin for TLS 1.3, giving up in-band upgrade
    decided_by: decision:macos-secure-transport
per_backend_files:
  darwin_default: "(tinygo || force_tinygo_logic) && darwin && !darwintls13"
  darwin_tls13: "(tinygo || force_tinygo_logic) && darwin && darwintls13"
  linux: "(tinygo || force_tinygo_logic) && linux"
  windows: "(tinygo || force_tinygo_logic) && windows"
  fallback: "(tinygo || force_tinygo_logic) && !darwin && !linux && !windows"
  vendored_c: >
    the mbedTLS sources in rule:mbedtls-vendoring carry the linux native tag
    too, so a std-go build never compiles them
rules:
  - exactly one dialTLS definition per build configuration
  - a catch-all file returns ErrPlatformNotSupported for unmatched GOOS
  - files importing crypto/tls carry "!tinygo", per requirement:no-crypto-tls-on-tinygo
  - cgo LDFLAGS stay in the per-OS file, never in a shared file
  - the public API is identical under every tag combination
precedent:
  - httpmux/mux_std.go
  - compress/zstd/writer_klauspost.go
cgo_flag_files: >
  cgo flags live in per-OS, per-compiler files because tinygo and host go need
  different ones; see rule:tinygo-cgo-flag-limits
documented_in: README section "Implementation selection"
