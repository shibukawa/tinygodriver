---
id: decision:websocket-fork
type: decision
title: gorilla/websocket Drop-in Fork
---
Fork system:gorilla-websocket v1.5.3 into `websocket/` as a drop-in with one import path that builds on both compilers, following decision:fasthttp-fork: applications name websocket types directly, so a re-export layer would break every release.

```yaml
state: accepted; implemented as websocket/
import_path: github.com/shibukawa/tinygodriver/websocket
mechanism: >
  vendor.py copies the root package from the module cache, rewrites import
  paths, applies exact-text patches with expected occurrence counts;
  PATCHES.md mirrors every patch, as in decision:fasthttp-router-vendoring
divergence: TinyGo-only, gated per rule:build-tag-selection; host Go stays upstream behaviour
license: directory is BSD-2-Clause (upstream's), repo is Apache-2.0
patch_sites: >
  one in the client TLS path, plus one seam in conn.go forced by
  rule:vendored-godebug-drift
retired_2026_09_02: >
  two of the original four went away with tinygo 0.42, which defines
  http.ProxyFromEnvironment -- returning "no proxy", exactly what the shim
  returned -- and (*tls.Config).Clone. Both call sites are back on upstream's
  own code and the shims are deleted
shim_shape:
  compat_std.go: clientTLS over crypto/tls
  compat_tinygo.go: >
    the same, plus ErrTLSUnsupported. clientTLS refuses rather than calling
    tls.Client, which no longer panics on tinygo 0.42 but returns the plaintext
    connection instead -- see requirement:no-crypto-tls-on-tinygo. That change
    made this patch more necessary, not less: unpatched upstream would now
    build and send the handshake in the clear
  deleted: tls_handshake.go and tls_handshake_116.go, folded into clientTLS
size_tag_candidate: >
  compress/flate is linked unconditionally even when compression is never
  enabled. Measured 81 KB of a 305 KB total. A websocket_nodeflate tag would
  mirror the fasthttp_nozstd lever in decision:fasthttp-fork
verified: >
  2026-08-10, tinygo 0.41.1 darwin/arm64. 27-check battery, 27/27 on tinygo,
  identical to host Go in every direction: tinygo to tinygo, tinygo client to Go
  server, Go client to tinygo server
```
