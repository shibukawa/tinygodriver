---
id: system:fasthttp-router
type: system
title: fasthttp/router (upstream)
---
Upstream HTTP router for fasthttp, radix-tree based, Julien Schmidt httprouter lineage. Cannot be used with decision:fasthttp-fork because it imports `github.com/valyala/fasthttp` directly; that import is the reason requirement:fasthttp-router-fork exists.

```yaml
module: github.com/fasthttp/router
version_pinned: v1.5.4
license: BSD-3-Clause; own lineage (Schmidt 2013, fasthttp org), distinct from fasthttp MIT and repo Apache-2.0
packages:
  root: router.go, group.go, path.go, types.go, utils.go
  radix: tree matching, the only subpackage
deps:
  valyala/fasthttp: declared v1.58.0; fork tracks v1.73.0, compile surface is small (RequestCtx, RequestHandler)
  savsgio/gotils: bytes.Rand, strconv B2S/S2B, strings helpers; pure Go
  valyala/bytebufferpool: already a repo dependency
stdlib_nontest: errors, fmt, io/fs, regexp, sort, strings, unicode/utf8
tinygo_risk:
  regexp: supported by TinyGo, adds binary size; used for {param:regex} routes
  none_else_expected: no crypto/tls, no os.File fast paths, no net use in non-test code
```
