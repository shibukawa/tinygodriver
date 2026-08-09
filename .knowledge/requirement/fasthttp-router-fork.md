---
id: requirement:fasthttp-router-fork
type: requirement
title: fasthttp/router Fork for TinyGo
---
Provide a fork of system:fasthttp-router that routes on top of decision:fasthttp-fork, so TinyGo applications get real routing instead of hand-rolled switch handlers. Upstream router imports `github.com/valyala/fasthttp` and therefore cannot see the fork; the import path is the only structural blocker.

```yaml
priority: must
scope:
  in:
    - root package and radix subpackage, imports rewritten to the fasthttp fork
    - builds and passes tests under both compilers from one import path
    - upstream test suite carried over and passing under TinyGo (netdev present)
  out:
    - behaviour changes or new features over upstream v1.5.4
    - forking savsgio/gotils or bytebufferpool; they stay module requirements
      unless a TinyGo incompatibility is proven
host_go_policy: >
  host-Go-only applications keep using upstream system:fasthttp-router with
  upstream fasthttp (user direction, 2026-08-09). The fork exists for TinyGo
  builds and for code shared across both compilers; on host Go the fork must
  stay behaviour-identical to upstream, patches gated on the tinygo tag,
  matching decision:fasthttp-fork.
acceptance:
  - fork + fasthttp fork example compiles with tinygo and go from the same source
  - route match, params, {param:regex}, wildcard, group, and 405/OPTIONS paths
    verified under TinyGo against host-Go reference output
  - host Go compat test proves parity with upstream v1.5.4 where practical
  - README + PATCHES-style record of every divergence, per decision:fasthttp-router-vendoring
verified: >
  2026-08-09, TinyGo 0.41.1 darwin/arm64. Full upstream suite passes under go
  test and tinygo test (fasthttp_nozstd and noasm both). examples/fasthttpserver
  now routes through the fork; curl battery (params, wildcard static, 405 with
  Allow, OPTIONS, 404, chunked stream) byte-identical on both compilers.
risk:
  fasthttp_version_skew: >
    upstream router declares fasthttp v1.58.0, the fork tracks v1.73.0; the
    only friction was in tests (missing Host headers), fixed by patch. See
    decision:fasthttp-router-vendoring
  regexp_size: >
    measured: the router adds 0.26 MB to the example's TinyGo binary
    (3.07 -> 3.33 MB with fasthttp_nozstd), router + radix + gotils + regexp
    together
```
