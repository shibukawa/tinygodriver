# Local patches to the vendored fasthttp/websocket

Vendored by `vendor.py` from `github.com/fasthttp/websocket v1.5.12`.

Every change below is applied by `vendor.py` itself, as an exact-text
replacement with an expected occurrence count. A count that no longer matches
aborts the run, so a version bump fails loudly instead of silently dropping a
patch. Keep this file and that script in sync.

Upstream is otherwise untouched — 28 sources, import paths rewritten only, and
every edit marked with a `PETITWEB:` comment so a diff against v1.5.12 stays
readable.

## Why any patch is needed

The import rewrite alone is what makes the fork necessary: upstream's
`FastHTTPUpgrader.Upgrade` takes upstream's `*fasthttp.RequestCtx`, and Go
treats that as a different type from the fork's, so no amount of aliasing lets
the two meet. That part is identical to `fasthttprouter`.

Unlike the router, this package also needs real patches. Five compile errors at
four sites, and not one of them is a design conflict — every one is a symbol
that upstream uses and TinyGo does not define:

| Missing | Site |
|---|---|
| `http.ProxyFromEnvironment` | `client.go` (`DefaultDialer`) |
| `tls.Conn`, as a *type* | `client.go` (`doHandshake`) |
| `(*tls.Config).Clone` | `client.go` (`cloneTLSConfig`) |
| `ConnectionState` on the connection `tls.Client` returns | `client.go` |
| `http.NewResponseController` | `server_utils.go` |

One more site is patched for behaviour rather than to compile: `tls.Client`
compiles on TinyGo and **panics** when called.

The replacements live in `compat_std.go` and `compat_tinygo.go`, constrained on
`tinygo` alone. `force_tinygo_logic` is deliberately not offered: every
divergence here is a missing standard-library symbol rather than alternate
logic, so a host-Go build has nothing to exercise, and `cloneTLSConfig` would
copy the mutex inside a real `tls.Config`.

Nothing on the **server** side needed patching. `server_fasthttp.go` — the
`FastHTTPUpgrader`, which is the whole reason to prefer this library over
gorilla here — compiles under TinyGo exactly as vendored, and so do `conn.go`,
`mask.go`, `compression.go` and the rest of the framing code.

## 1. `client.go` — the environment proxy

`DefaultDialer.Proxy` was `http.ProxyFromEnvironment`. TinyGo's
`http.Transport` is an empty struct and that function does not exist.

`defaultProxy` replaces it. On standard Go it *is*
`http.ProxyFromEnvironment`. On TinyGo it calls
`golang.org/x/net/http/httpproxy`, which is the package standard Go's own
implementation delegates to, behind the same `sync.Once`. So the variables
read, their precedence, the `NO_PROXY` matching and the loopback exemption are
not a reimplementation — they are the same code.

Ignoring the environment instead was the alternative, and it is the wrong one
for the same reason `https/proxy.go` gives: a machine behind a mandatory proxy
would fail to connect with nothing in the error pointing at a proxy.

The cost is 95 KB of TinyGo binary, and only for a program that dials. A
server-only build never reaches `DefaultDialer`, and the linker drops all of it
— measured: zero difference in `examples/fasthttpserver`.

## 2. `client.go` — originating TLS

`tlsClientHandshake` replaces `tls.Client` plus the handshake and its two
`httptrace` calls, inline. Three things there are unavailable on TinyGo:
`tls.Conn` is not a type, `(*tls.Config).Clone` is not a method, and the
connection `tls.Client` returns has no `ConnectionState`.

Worse than any of them, `tls.Client` **panics** rather than failing. A stub that
compiles is worse than one that does not, because nothing warns you until a dial
is in flight, and a panic inside a dialer is not something an application can
defend against. `tlsClientHandshake` returns `ErrTLSUnsupported` instead, and
returns the *unwrapped* connection with it so the caller's deferred `Close`
still reaches the socket.

`cloneTLSConfig` and `doHandshake` move whole into the compat files for the
same reason. The TinyGo clone lists the fields rather than copying the struct,
because `tls.Config` carries a `sync.RWMutex` there too.

**This is not the end of `wss://` on TinyGo.** Upstream already provides the
hook: a `Dialer.NetDialTLSContext` returning a connection the OS has already
encrypted skips this path entirely.

```go
d := &websocket.Dialer{
    NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
        return net.DialTLS(addr)
    },
}
```

A `wss://` **server** has no equivalent escape hatch: terminating TLS needs
`tls.Server` and `tls.X509KeyPair`, neither of which TinyGo defines. Put a
terminator in front, exactly as the fasthttp fork's README says.

## 3. `server_utils.go`, `server_utils_119.go` — hijacking through `net/http`

Upstream picks between two `HijackResponse` implementations by Go version:
`http.NewResponseController(w).Hijack()` from 1.20, and the older type assertion
to `http.Hijacker` before that. TinyGo defines `http.Hijacker` and implements it
on its `response`, but has no `NewResponseController`.

The two build constraints gain `&& !tinygo` and `|| tinygo`, so TinyGo takes the
pre-1.20 path. Nothing else changes; `http.Hijacker` is what
`NewResponseController.Hijack` reaches for anyway.

This makes the `net/http` `Upgrader` *compile* on TinyGo. It does not make it
work: `net/http`'s own `Hijack` deadlocks under netdev, because `serve` starts a
background read before every bodyless-GET handler and `hijackLocked` then waits
for a read that a socket call already in flight will never abandon. Use
`FastHTTPUpgrader`; see the README.

## 4. `client_server_test.go` — constrained to standard Go

Upstream's integration suite is gorilla's, and it builds TLS servers through
`net/http/httptest` and keeps cookies in `net/http/cookiejar`. TinyGo ships
neither `cookiejar` nor a `tls.Server` for `httptest.NewTLSServer` to use, so
the file carries `//go:build !tinygo`. Twenty-four tests are host-Go only as a
result.

Every other upstream test file is vendored unchanged and runs under both
compilers.

The gap is not left open. `compat_test.go`, hand-written, covers the fasthttp
side over real sockets on both compilers — echo across every frame-length
encoding, fragmentation, ping/pong, the close handshake, subprotocol and
compression negotiation, the handshake rejections, an upgrade sharing a listener
with ordinary HTTP, and eight concurrent connections. Upstream has **no tests at
all** for `FastHTTPUpgrader`, so this is new coverage rather than a
reimplementation of something lost.

## Not patched, worth knowing

- `FastHTTPUpgrader.responseError` sets `Sec-Websocket-Version: 13` and then
  calls `ctx.Error`, which resets the response and drops the header again. RFC
  6455 asks for it on a rejected handshake. This is upstream's behaviour,
  identical on both compilers, so it is upstream's to fix; `TestFastHTTPRejects`
  pins it so a version that fixes it says so.
- Mask keys come from `math/rand`, not `crypto/rand`, on both compilers.
- Payload UTF-8 is not validated; `doc.go` says the application must.
