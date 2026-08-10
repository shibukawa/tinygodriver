---
id: requirement:fasthttp-websocket-fork
type: requirement
title: fasthttp/websocket Fork for TinyGo
---
Provide a fork of system:fasthttp-websocket that upgrades connections from decision:fasthttp-fork, so a fasthttp application on TinyGo gets WebSocket from an ordinary route. Upstream imports `github.com/valyala/fasthttp` and therefore cannot see the fork; separately, rule:nethttp-hijack-deadlock is why `FastHTTPUpgrader` rather than gorilla's `Upgrader` is the entry point, and why this library is the right one to fork.

Sibling of requirement:websocket-fork + requirement:httpserver-package, which reach the same place for `net/http` applications by routing upgrades around `http.Server`. Both shipped 2026-08-10; neither supersedes the other, and an application picks by which HTTP server it already uses.

```yaml
priority: must
scope:
  in:
    - one flat package, imports rewritten to the fasthttp fork
    - builds and passes tests under both compilers from one import path
    - upstream tests carried over where TinyGo can run them
    - first-ever coverage of the fasthttp upgrade path, on both compilers
  out:
    - behaviour changes or new features over upstream v1.5.12
    - fixing upstream bugs that are not TinyGo-specific; pin them in tests instead
    - forking klauspost/compress, savsgio/gotils or golang.org/x/net; they stay
      module requirements
host_go_policy: >
  host-Go-only applications keep using upstream system:fasthttp-websocket with
  upstream fasthttp, matching requirement:fasthttp-router-fork. On host Go the
  fork must stay behaviour-identical to upstream, patches gated on the tinygo
  tag, matching decision:fasthttp-fork.
acceptance:
  - fork + fasthttp fork example compiles with tinygo and go from the same source
  - echo, fragmentation, ping/pong, close handshake, subprotocol and compression
    negotiation, handshake rejections and concurrency verified under TinyGo
  - wire format checked against an implementation sharing no code with the library
  - README + PATCHES-style record of every divergence, per
    decision:fasthttp-websocket-vendoring
verified: >
  2026-08-10, TinyGo 0.41.1 / host Go 1.26.5, darwin/arm64.
  tinygo test: 54 pass 0 fail (fasthttp_nozstd and noasm). go test: 78 pass
  0 fail, race-clean. A hand-written RFC 6455 client sharing no code with the
  library passes 17/17 against both the TinyGo and the host-Go server -- 
  handshake and accept key, echoes at 0/1/125/126/127/1024/65535/65536/200000
  bytes, UTF-8 text, three-frame fragmentation, ping/pong, close handshake.
  All four compiler directions pass 14/14. examples/fasthttpserver serves /ws
  alongside its routes on both compilers.
risk:
  fasthttp_version_skew: >
    upstream declares fasthttp v1.58.0, the fork tracks v1.73.0; no friction
    found, unlike the router, whose tests needed Host headers
  upgrader_confusion: >
    the net/http Upgrader compiles on TinyGo and then deadlocks
    (rule:nethttp-hijack-deadlock). Nothing can make it fail at compile
    time without diverging from upstream's API, so the README and PATCHES.md
    carry the warning instead
  binary_size: >
    measured: 105 KB added to examples/fasthttpserver's TinyGo binary
    (3.33 -> 3.44 MB with fasthttp_nozstd), against gorilla's 305 KB, because
    fasthttp already links compress/flate. A client pays another 95 KB for
    x/net/http/httpproxy; a server-only build pays zero for it, byte for byte
```
