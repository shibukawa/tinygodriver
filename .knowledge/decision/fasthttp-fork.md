---
id: decision:fasthttp-fork
type: decision
title: fasthttp Drop-in Fork
---
Fork valyala/fasthttp v1.73.0 into `fasthttp/` as a drop-in with one import path that builds on both compilers, instead of a selecting wrapper: applications name fasthttp types directly, so a re-export layer would break every release.

```yaml
state: accepted; shipped, merged a6efdae
import_path: github.com/shibukawa/tinygodriver/fasthttp
mechanism: >
  fasthttp/vendor.py copies root + fasthttputil + stackless from the module
  cache, rewrites import paths, applies exact-text patches with expected
  occurrence counts; PATCHES.md mirrors every patch. clean() deletes everything
  not in LOCAL_FILES, directories included.
divergence: TinyGo-only, gated on `tinygo` build tag; host Go is upstream behaviour for behaviour
license: directory is MIT (upstream's), repo is Apache-2.0
size_tags: fasthttp_nozstd drops klauspost zstd (halves binary, removes noasm need)
verified: 53-check battery byte-identical on both compilers, darwin arm64
```
