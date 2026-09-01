# Local patches to the vendored gorilla/websocket

Vendored by `vendor.py` from `github.com/gorilla/websocket v1.5.3`.

Every change below is applied by `vendor.py` itself, as an exact-text
replacement with an expected occurrence count. A count that no longer matches
aborts the run, so a version bump fails loudly instead of silently dropping a
patch. Keep this file and that script in sync.

## What the fork is for

Upstream cannot be used under TinyGo as it stands. Its client TLS path calls
`tls.Client` and expects a handshake; TinyGo 0.42 compiles that call and hands
back the **plaintext** connection, so an unpatched build would send the request
in the clear rather than fail. Applications name `websocket.Conn`,
`websocket.Upgrader` and `websocket.Dialer` directly, so a re-export wrapper
would break at every release; the fork is the drop-in itself, as in
`../fasthttp`.

Two further sites were patched until TinyGo 0.42, which now defines
`http.ProxyFromEnvironment` — returning "no proxy", which is exactly what the
shim returned — and `(*tls.Config).Clone`. Both call sites are back on
upstream's own code, and the shims are gone.

Nothing else needed touching. There is no assembly, so no `noasm` tag, and no
external dependencies to fork: the SOCKS5 support in `x_net_proxy.go` is
vendored by upstream already.

Under standard Go every patch below resolves to upstream behaviour, through
`compat_std.go`. The divergence lives in `compat_tinygo.go`.

## The server needs `httpserver`

This is not a patch, but it is the thing to know before using the fork.
`Upgrader.Upgrade` asks the `http.ResponseWriter` for `http.Hijacker`, and
under TinyGo `net/http`'s own `Hijack` never returns: the server starts a
background read before calling the handler and cancels it by moving the read
deadline into the past, which netdev cannot do to a `recv()` already in flight.
The handshake hangs with no error and no log line.

Serve through `github.com/shibukawa/tinygodriver/httpserver` instead of
`http.Server.Serve`. Nothing in this directory works around it, because the
defect is not in this library.

## 1. `client.go` — the TLS handshake

One block, replaced by a single call to `clientTLS`, which returns the
connection, the negotiated state and the error.

The reason changed with TinyGo 0.42 and got worse. Before, `tls.Client` panicked
and `ConnectionState` was not a method on what a dial returns, so upstream did
not build. Now it builds: `tls.Client` is `return &Conn{Conn: conn}`, and
`Handshake` reports success. Unpatched upstream would therefore complete a
`wss://` dial over a **plaintext** socket and send the request in the clear.
`clientTLS` returns `ErrTLSUnsupported` instead, which is the only safe answer
when the handshake belongs to the device.

The connection is returned even on failure, because upstream assigned
`tls.Client`'s result to `netConn` before handshaking so that `DialContext`'s
deferred close still had something to close. Preserving that is why `clientTLS`
returns a connection rather than only an error.

`tls_handshake.go` and `tls_handshake_116.go` are dropped rather than patched.
They select a handshake by Go version, which is the wrong axis here: the
handshake differs by compiler. Both are folded into `clientTLS`.

On TinyGo `clientTLS` returns `ErrTLSUnsupported` rather than attempting
anything. A `wss://` client works by handing the dialer a connection that is
already through the handshake:

```go
d := websocket.Dialer{
	NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.DialTLS(addr)
	},
}
```

A `wss://` server is not possible at all: TinyGo defines neither `tls.Server`
nor `X509KeyPair`, and its `http.Server` has no `ServeTLS`. Terminate TLS in
front of the process.

## 2. `conn.go` — a seam for the frame mask

`newMaskKey` calls `rand.Uint32` directly. It now calls `maskKeySource`, a
package variable initialised to `rand.Uint32`. Behaviour is unchanged and only
a test in this package can reach the variable.

The reason is `prepared_test.go`, which compares a normal write against a
prepared one byte for byte and calls `rand.Seed(1234)` to make the client's
frame mask reproducible. `math/rand.Seed` became a no-op in Go 1.24. Upstream's
own test run escapes that only because its `go.mod` says `go 1.12` and GODEBUG
defaults follow the module's Go version; vendored into a module that says
`go 1.27`, the seed does nothing and all five client cases fail.

A `//go:debug randseednop=0` directive fixes the host build and does nothing
under TinyGo, which does not implement godebug. Replacing the source outright
is the only fix that works on both compilers. `prepared_test.go` is patched to
call `setFixedMaskKey`, defined in the hand-written `compat_test.go`.

This is the one patch to a non-test source that exists only for a test. It
earns its place: without it the fork loses the check that prepared messages and
normal writes produce identical bytes, on exactly the masked path that matters
most under TinyGo.

## Files not vendored

`examples/` is a directory of standalone programs with their own dependencies.

`client_server_test.go` and `client_test.go` are dropped. The first terminates
TLS, which TinyGo cannot do; the second asserts on `http.ProxyFromEnvironment`,
which patch 1 replaces. What they covered on the plaintext path is covered by
`integration_test.go`, which runs against real sockets through `httpserver` on
both compilers.
