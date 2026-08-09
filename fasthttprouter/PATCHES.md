# Local patches to the vendored fasthttp/router

Vendored by `vendor.py` from `github.com/fasthttp/router v1.5.4`.

Every change below is applied by `vendor.py` itself, as an exact-text
replacement with an expected occurrence count. A count that no longer matches
aborts the run, so a version bump fails loudly instead of silently dropping a
patch. Keep this file and that script in sync.

## Why the non-test sources carry no patches

The fork exists for one reason: upstream imports `github.com/valyala/fasthttp`,
and a router built against upstream fasthttp cannot serve the fork at
`github.com/shibukawa/tinygodriver/fasthttp` — the two `RequestHandler` types
are distinct even though they are textually identical. Rewriting the imports is
the entire fork.

Nothing else needed touching: the router's non-test sources use `errors`,
`fmt`, `io/fs`, `regexp`, `sort`, `strings` and `unicode/utf8`, none of the
symbols TinyGo's stub `crypto/tls` or its `os.File` are missing. All 19 sources
compile under TinyGo as vendored, and the full upstream test suite runs under
`tinygo test` — which is also why the tests are vendored here at all, where the
fasthttp fork replaced its TLS-heavy suite with a hand-written battery.

Both patches below are to those tests, not to the router.

## 1. `router_test.go`, `group_test.go` — Host headers

Upstream wrote its raw requests against fasthttp v1.58; v1.73 rejects an
HTTP/1.1 request without a Host header, and every one of the tests' hand-built
requests lacked one, failing on both compilers alike. `Host: test` is inserted
into all of them: 3 requests in `router_test.go`, 10 in `group_test.go`.

## 2. `router_test.go` — handler identity without reflect

`TestRouterMutable` proves an immutable route kept its original handler by
comparing `reflect.ValueOf(h).Pointer()`. TinyGo does not implement `Pointer()`
reliably for funcs: a handler stored into the radix tree and looked up again
calls the correct function, yet reflects to a pointer unrelated to the
original's — TinyGo's func value is a `{context, code}` pair, and the context
word is not even stable for non-capturing closures (a static address, `0x1` and
`nil` were all observed for the same handler).

The two comparisons are replaced with `sameHandler`, a helper this directory
provides in two build-tagged variants:

- `handleridentity_std_test.go` (`!tinygo`) — upstream's own reflect
  comparison, hoisted into the helper.
- `handleridentity_tinygo_test.go` (`tinygo`) — compares the code word of the
  two func values through `unsafe`, which is the function itself. Closures over
  one literal with different captured state would compare equal; the test's
  handlers capture nothing.
