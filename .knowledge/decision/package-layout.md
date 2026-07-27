---
id: decision:package-layout
type: decision
title: Package Layout and Import Path
---
The package lives at repository root as `https`, matching the flat layout of the existing `httpmux` and `httprevproxy` packages.

```yaml
state: accepted
import_path: github.com/shibukawa/tinygodriver/https
package_name: https
rejected:
  net_https: extra nesting with no sibling packages to justify it
  inside_netdev: >
    system:tinygo-netdev implements a socket driver; an http client belongs
    above that layer
files:
  shared:
    - doc.go
    - client.go: api:https-functions
    - transport.go: api:https-transport
    - config.go: data:https-config
    - errors.go: requirement:error-classification
  std_path:
    - roundtrip_std.go: "!tinygo && !force_tinygo_logic"
  native_shared:
    - roundtrip_native.go: "tinygo || force_tinygo_logic"
  darwin:
    - dial_darwin.go
    - tls_darwin.h, tls_darwin.c
    - cgoflags_darwin_tinygo.go, cgoflags_darwin_hostgo.go
    note: >
      the cgo flags are split because tinygo needs literal SDK paths and host
      go must not have them; see rule:tinygo-darwin-toolchain
  linux:
    - dial_linux.go
    - tls_mbedtls.h, tls_mbedtls.c
    - cgoflags_linux.go
    - internal/mbedtls/: vendored sources, see rule:mbedtls-vendoring
    note: >
      cgo compiles C only from the package directory, so the vendored sources
      cannot simply be imported from a subpackage; the include path is wired in
      cgoflags_linux.go
  windows:
    - dial_windows.go: pure Go, no cgo
  fallback:
    - dial_unsupported.go
  see: rule:build-tag-selection
