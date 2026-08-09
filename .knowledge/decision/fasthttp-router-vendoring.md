---
id: decision:fasthttp-router-vendoring
type: decision
title: fasthttp/router Vendoring Approach
---
Vendor system:fasthttp-router the same way decision:fasthttp-fork is vendored: a python script copies upstream from the module cache, rewrites imports, and applies exact-text patches with expected occurrence counts, recorded in a PATCHES.md.

```yaml
state: shipped 2026-08-09; full upstream suite passes under go test and tinygo test
location:
  path: fasthttprouter/
  import_path: github.com/shibukawa/tinygodriver/fasthttprouter
  package_name: router, unchanged from upstream; only the directory is named fasthttprouter
  why: >
    flat top-level like httpmux and httprevproxy (decision:package-layout); no
    interaction with fasthttp/vendor.py clean(), which deletes any directory
    under fasthttp/ not in LOCAL_FILES. Own vendor.py lives in the directory.
  rejected:
    fasthttp_router_subdir: clean() coupling; two vendor scripts fighting over one tree
    top_level_router_dir: "router/" alone says nothing about what it routes for
import_rewrite:
  - github.com/fasthttp/router -> github.com/shibukawa/tinygodriver/fasthttprouter
  - github.com/valyala/fasthttp -> github.com/shibukawa/tinygodriver/fasthttp
copy: root package + radix; _examples, go.mod, go.sum dropped
license: carry upstream BSD-3 LICENSE in the fork directory; third licence flavour in repo after Apache-2.0 and fasthttp MIT
patches_found: >
  zero in non-test sources, as predicted; the import rewrite is the whole fork.
  Two test-only patches: Host headers on 13 raw requests (upstream wrote them
  against fasthttp v1.58, v1.73 requires Host), and TestRouterMutable's
  reflect-based handler identity replaced by a sameHandler helper because of
  rule:tinygo-reflect-func-pointer. Helper ships as two build-tagged local
  files in LOCAL_FILES.
tests: vendored along with the sources, unlike the fasthttp fork, whose suite was TLS-heavy; the router's is pure logic and runs under tinygo test
deps_policy: gotils and bytebufferpool stay ordinary module requirements, as brotli and klauspost do for the fasthttp fork
```
