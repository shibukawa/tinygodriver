# fasthttpwebsocket

A fork of [fasthttp/websocket](https://github.com/fasthttp/websocket) v1.5.12 —
itself a fork of gorilla/websocket — that upgrades connections from this
repository's [fasthttp fork](../fasthttp/). It is a drop-in replacement: change
the import path and nothing else. The package is still named `websocket`, so
code reads exactly as it does with upstream.

```go
import websocket "github.com/shibukawa/tinygodriver/fasthttpwebsocket"
```

Upstream cannot be used here even though it compiles under standard Go: its
`FastHTTPUpgrader.Upgrade` takes `github.com/valyala/fasthttp`'s
`*RequestCtx`, and Go treats that as a different type from the fork's. Beyond
the import rewrite, five compile errors and one panicking stub needed patching;
[PATCHES.md](./PATCHES.md) records all of them. The whole server side —
`FastHTTPUpgrader` and the framing code under it — needed **no patches at all**.

If an application targets standard Go only, use upstream `fasthttp/websocket`
with upstream fasthttp; this fork is for TinyGo builds and for code shared
across both compilers.

## Licence

**This directory is BSD-3-Clause**, not the Apache 2.0 of the rest of the
repository nor the MIT of `fasthttp/`. `LICENSE` and `AUTHORS` are upstream's
own, and the copyright is the Gorilla WebSocket Authors'.

## Quick start

The build flags are the fasthttp fork's, since that is what pulls in the
compression stack: `-tags fasthttp_nozstd` (recommended) or `-tags noasm`
under TinyGo, nothing under standard Go.

```go
package main

import (
	"github.com/shibukawa/tinygodriver/fasthttp"
	websocket "github.com/shibukawa/tinygodriver/fasthttpwebsocket"
	_ "github.com/shibukawa/tinygodriver/netdev"
)

var upgrader = websocket.FastHTTPUpgrader{}

func main() {
	fasthttp.ListenAndServe(":8080", func(ctx *fasthttp.RequestCtx) {
		upgrader.Upgrade(ctx, func(conn *websocket.Conn) {
			for {
				mt, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if conn.WriteMessage(mt, msg) != nil {
					return
				}
			}
		})
	})
}
```

`Upgrade` returns as soon as the 101 is queued. fasthttp writes the response,
hands the connection over, runs the handler, and closes the connection when it
returns — set `Server.KeepHijackedConns` to keep it open past that.

`examples/fasthttpserver` serves `/ws` this way alongside its ordinary routes,
on both compilers.

## Use `FastHTTPUpgrader`, not `Upgrader`

Both compile under TinyGo. Only one works.

`Upgrader`, the `net/http` one, needs `http.ResponseWriter`'s `Hijack`, and
**that deadlocks under netdev**. `net/http`'s `serve` starts a background read
before every bodyless-GET handler; `hijackLocked` then calls
`abortPendingRead`, which pushes the read deadline into the past and waits for
the background read to notice. It never notices — netdev's `waitFD` returns
immediately for a zero deadline and the read then blocks inside a plain
`recv()`, and the Netdever interface takes the deadline **by value at call
time**, so a later `SetReadDeadline` cannot reach a call already in flight. No
protocol upgrade of any kind can work on TinyGo's `net/http` server.

`FastHTTPUpgrader` sidesteps it because `RequestCtx.Hijack` is a synchronous
handoff: fasthttp finishes the response, stops reading, and calls the handler on
the same goroutine. There is no background read to abort. That is the finding
this fork rests on, and `compat_test.go` is what proves it — the whole battery
runs over real sockets, so on TinyGo it runs over netdev.

An application already on `net/http` does not need to move to fasthttp for
this. [`websocket`](../websocket/) with [`httpserver`](../httpserver/) reaches
the same place by routing upgrades around `http.Server` and keeping a real one
for everything else, on the same port. The two are siblings; pick by which HTTP
server the application already uses. What this fork saves is that layer.

## Verification

| | TinyGo 0.41.1 | host Go 1.26.5 |
|---|---|---|
| `go`/`tinygo test ./fasthttpwebsocket` | 54 pass, 0 fail | 78 pass, 0 fail |

The 24-test difference is `client_server_test.go`, gorilla's `net/http`
integration suite, which needs `net/http/cookiejar` and TLS `httptest` servers
that TinyGo does not have. Every other upstream test file runs under both.

Beyond the suite, on darwin/arm64:

- **A hand-written RFC 6455 client that shares no code with the library** —
  raw sockets, frames built by hand — passes 17/17 against the TinyGo server and
  17/17 against the host-Go one: handshake and `Sec-WebSocket-Accept`, echoes at
  0/1/125/126/127/1024/65535/65536/200000 bytes, UTF-8 text, a three-frame
  fragmented message, ping/pong, and the close handshake.
- **All four compiler directions** pass 14/14 — TinyGo↔TinyGo, TinyGo client to
  host-Go server, host-Go client to TinyGo server, host-Go↔host-Go.

Loopback echo throughput came out at 35–49k round trips/s at 64 B on both
compilers, with too much run-to-run variance on this host to separate them; the
larger sizes varied by more than the gap between the two, so no number is quoted
for them.

## Cost

Adding `/ws` to `examples/fasthttpserver` (`-tags fasthttp_nozstd`,
darwin/arm64) grew the TinyGo binary from 3.33 MB to 3.44 MB. **105 KB** — much
less than the 305 KB the [`websocket`](../websocket/) fork costs on the
`net/http` side, because `compress/flate` is already linked by fasthttp's own
compression rather than being dragged in by the WebSocket library. The 81 KB
that fork weighed against a `nodeflate` tag is already paid for here.

A program that **dials** pays another 95 KB for `x/net/http/httpproxy`, which
is how `Dialer.Proxy` reads `HTTP_PROXY` on TinyGo (see PATCHES.md §1). A
server-only build pays exactly zero for it — measured: the linker drops all of
it, byte for byte.

## Limitations on TinyGo

- **`wss://` server: impossible.** Terminating TLS needs `tls.Server` and
  `tls.X509KeyPair`, neither of which TinyGo defines. Put a terminator in front.
- **`wss://` client: works, with a hook.** Set `Dialer.NetDialTLSContext` to
  return `net.DialTLS(addr)`. Without it the dial fails with
  `ErrTLSUnsupported` rather than panicking inside `tls.Client`, which is what
  upstream would do.
- **`Upgrader` (the `net/http` one) deadlocks**, as above.
- **`-scheduler=threads`** is what netdev needs whenever goroutines block in
  socket calls, which every connection here does. It is the default on darwin
  and Linux; `-scheduler=tasks` is a known-unsupported configuration.

## Vendoring

`python3 vendor.py` re-vendors from the module cache after bumping
`WEBSOCKET_VERSION` — run `go mod download github.com/fasthttp/websocket@<version>`
first. The script copies the package, tests included, rewrites the imports, and
applies the PATCHES.md edits with exact-occurrence checks, then gofmts the
result.

`compat_std.go`, `compat_tinygo.go` and the three `compat*_test.go` files are
hand-written and survive re-vendoring; everything else in this directory is
generated.
