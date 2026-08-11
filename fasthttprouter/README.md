# fasthttprouter

A fork of [fasthttp/router](https://github.com/fasthttp/router) v1.5.4 that
routes for this repository's [fasthttp fork](../fasthttp/). It is a drop-in
replacement: change the import path and nothing else. The package is still
named `router`, so code reads exactly as it does with upstream.

```go
import router "github.com/shibukawa/tinygodriver/fasthttprouter"
```

Upstream cannot be used here even though it compiles under standard Go:
it registers handlers against `github.com/valyala/fasthttp`, and Go treats its
`RequestHandler` and the fork's as unrelated types. This fork is that one
import rewritten — the non-test sources carry **zero patches** beyond it, and
[PATCHES.md](./PATCHES.md) records the two test-only fixes. The full upstream
test suite is vendored and passes under both compilers.

If an application targets standard Go only, use upstream `fasthttp/router`
with upstream fasthttp; this fork is for TinyGo builds and for code shared
across both compilers.

## Licence

**This directory is BSD-3-Clause**, not the Apache 2.0 of the rest of the
repository nor the MIT of `fasthttp/`. `LICENSE` is upstream's own.

## Quick start

No build tags are needed on either compiler. The fasthttp fork pulls in the
compression stack, and it encodes zstd through `compress/zstd` under TinyGo;
`-tags fasthttp_nozstd` drops zstd if the last 0.05 MB matters.

```go
package main

import (
	"fmt"

	"github.com/shibukawa/tinygodriver/fasthttp"
	router "github.com/shibukawa/tinygodriver/fasthttprouter"
	_ "github.com/shibukawa/tinygodriver/netdev"
)

func main() {
	r := router.New()
	r.GET("/hello/{name}", func(ctx *fasthttp.RequestCtx) {
		fmt.Fprintf(ctx, "hello, %s!\n", ctx.UserValue("name"))
	})
	fasthttp.ListenAndServe(":8080", r.Handler)
}
```

`examples/fasthttpserver` uses this router on both compilers, wildcard static
route included.

## Cost

Adding the router to that example's TinyGo build (`-tags fasthttp_nozstd`,
darwin/arm64) grew the binary from 3.07 MB to 3.33 MB. The 0.26 MB is the
router, its radix tree, `savsgio/gotils`, and `regexp` — which the router pulls
in for `{param:regex}` routes whether or not any route uses one.

## Vendoring

`python3 vendor.py` re-vendors from the module cache after bumping
`ROUTER_VERSION` — run `go mod download github.com/fasthttp/router@<version>`
first. The script copies the root package and `radix`, tests included, rewrites
the imports, and applies the PATCHES.md edits with exact-occurrence checks.

TinyGo quirk worth knowing if the tests ever grow: `reflect.Value.Pointer()` on
a func is unreliable there, which is why handler identity goes through the
`sameHandler` helper — see PATCHES.md §2.
