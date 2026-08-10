---
id: decision:fasthttp-websocket-vendoring
type: decision
title: fasthttp/websocket Vendoring Approach
---
Vendor system:fasthttp-websocket the way decision:fasthttp-router-vendoring does: a python script copies upstream from the module cache, rewrites imports, and applies exact-text patches with expected occurrence counts, recorded in a PATCHES.md. Fork fasthttp/websocket rather than gorilla/websocket, because rule:nethttp-hijack-deadlock rules out gorilla's only server entry point on TinyGo.

```yaml
state: shipped 2026-08-10; 54 tests pass under tinygo test, 78 under go test
location:
  path: fasthttpwebsocket/
  import_path: github.com/shibukawa/tinygodriver/fasthttpwebsocket
  package_name: websocket, unchanged from upstream; only the directory is named fasthttpwebsocket
  why: flat top-level beside fasthttprouter (decision:package-layout); own vendor.py in the directory
import_rewrite:
  - github.com/fasthttp/websocket -> github.com/shibukawa/tinygodriver/fasthttpwebsocket
  - github.com/valyala/fasthttp -> github.com/shibukawa/tinygodriver/fasthttp
copy: the one flat package, tests included; _examples, go.mod, go.sum dropped
license: carry upstream BSD-3 LICENSE and AUTHORS; same flavour as fasthttprouter
why_not_gorilla: >
  gorilla/websocket has no external dependencies and no assembly, which makes it
  the cheaper fork on paper. It offers only the net/http Upgrader, and that
  cannot complete an upgrade on TinyGo at all (rule:nethttp-hijack-deadlock), so
  it needs a demultiplexer in front of http.Server -- which is exactly what
  decision:websocket-fork plus decision:httpserver-demux built, and it is a
  package's worth of code. fasthttp/websocket is gorilla plus a fasthttp
  upgrader, so on the fasthttp stack that layer is not needed at all: one extra
  dependency instead of an extra package.
relationship_to_websocket_fork: >
  not a duplicate and not a replacement. decision:websocket-fork serves net/http
  applications; this serves fasthttp ones. They landed the same day, share the
  same root diagnosis, and the fasthttp side is the cheaper of the two only
  because fasthttp's own Hijack is well behaved.
patches_found: >
  five compile errors at four sites, all missing symbols, all in client.go and
  server_utils.go. Zero on the server side: FastHTTPUpgrader and the framing
  code compile as vendored. One extra patch for behaviour, because TinyGo's
  tls.Client compiles and panics. Test-only: client_server_test.go constrained
  to !tinygo for net/http/cookiejar and TLS httptest.
proxy_shim: >
  http.ProxyFromEnvironment does not exist on TinyGo. Rather than reimplement
  the variable precedence, defaultProxy calls golang.org/x/net/http/httpproxy,
  which is the package standard Go's own implementation delegates to. Same code,
  same behaviour, 95 KB, and only for builds that dial. Ignoring the environment
  was rejected for the reason https/proxy.go already records.
new_dependency: >
  golang.org/x/net, for proxy (SOCKS5, upstream's own import) and http/httpproxy
  (the shim). First use in this repository.
tests: >
  upstream's vendored, minus one file; plus compat_test.go, hand-written, which
  is the only coverage the fasthttp upgrade path has anywhere -- upstream has
  none. It runs over real sockets, so under tinygo test it runs over netdev,
  which is what makes it a proof of rule:nethttp-hijack-deadlock's remedy
  rather than a unit test.
deps_policy: klauspost/compress, gotils and x/net stay ordinary module requirements, as for the fasthttp and router forks
```
