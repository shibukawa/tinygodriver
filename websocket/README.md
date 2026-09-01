# websocket

A fork of [gorilla/websocket](https://github.com/gorilla/websocket) v1.5.3 that
builds under TinyGo.

```go
import "github.com/shibukawa/tinygodriver/websocket"
```

The API is upstream's, unchanged. Under standard Go the behaviour is upstream's
too; the divergences are gated on the `tinygo` build tag and listed in
[PATCHES.md](./PATCHES.md).

Upstream needs two edits: one because TinyGo's `tls.Client` returns the
plaintext connection instead of handshaking, and one seam so a test can fix the
frame mask. Two more were needed until TinyGo 0.42, which now defines
`http.ProxyFromEnvironment` and `(*tls.Config).Clone`. There is no assembly, so
no `noasm` tag, and no external dependencies: upstream vendors its own SOCKS5
support.

## The server needs `httpserver`

`Upgrader.Upgrade` asks the `http.ResponseWriter` for `http.Hijacker`. Under
TinyGo, `net/http`'s own `Hijack` never returns — the server starts a
background read before calling the handler and cancels it by moving the read
deadline into the past, which netdev cannot do to a `recv()` already in flight.
The handshake hangs with no error and no log line.

Serve through [`httpserver`](../httpserver) rather than `http.Server.Serve`:

```go
mux := http.NewServeMux()
mux.HandleFunc("/healthz", healthz)
mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	for {
		mt, msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		if err := c.WriteMessage(mt, msg); err != nil {
			return
		}
	}
})

ln, err := net.Listen("tcp", ":8080")
if err != nil {
	return err
}
return httpserver.Serve(ln, &http.Server{Handler: mux})
```

The WebSocket route is one endpoint among many on one port. Nothing in this
package works around the deadlock, because the defect is not in this library.

## Clients

`ws://` needs nothing special.

`wss://` needs a dialer that returns a connection already through the
handshake, because the handshake belongs to the OS. TinyGo's
`crypto/tls.Client` does not perform one — it returns the connection you gave
it and reports success — so this fork refuses rather than letting a `wss://`
dial succeed in cleartext.

```go
d := websocket.Dialer{
	NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.DialTLS(addr) // netdev performs the handshake
	},
}
c, resp, err := d.Dial("wss://example.com/ws", nil)
```

Without that hook a `wss://` dial returns `ErrTLSUnsupported` rather than
panicking. The sentinel is declared on both build paths, so a caller can
compare against it without build tags of its own; the standard build never
returns it.

On darwin, extra trust anchors come from `SSL_CERT_FILE`, which netdev honours.
Note that Go on darwin *ignores* `SSL_CERT_FILE` and verifies through the
platform verifier, so a test comparing the two compilers must pass `RootCAs`
directly on the host side. macOS also rejects a server certificate with no
`serverAuth` extended key usage, reporting OSStatus -9807.

A `wss://` **server** is not possible: TinyGo defines neither `tls.Server` nor
`X509KeyPair`, and its `http.Server` has no `ServeTLS`. Terminate TLS in front.

## What is verified

The upstream test suite runs as vendored, on all three layers, plus an
integration suite over real sockets covering echo of text, binary and empty
messages, every payload length 0..600 and each frame-length boundary,
deliberately unaligned buffers, fragmented writes, streaming reads, a 4 MiB
message, ping and pong in both directions, the close handshake with code and
text, the read limit, permessage-deflate including that it shrinks the wire,
subprotocol negotiation, origin rejection, and 16 concurrent clients.

The length sweep and the unaligned buffers exist for `mask.go`, which does raw
`uintptr` word arithmetic. It is correct under TinyGo, but it is the code most
worth re-checking after a toolchain bump.

## Upstream behaviour worth knowing

Neither of these is a TinyGo concern — both are identical on both compilers —
but both surprise people.

**Payload UTF-8 is not validated.** `doc.go` puts that on the application. Only
close-frame text is checked.

**Mask keys come from `math/rand`, not `crypto/rand`.** They are auto-seeded, so
they do vary per process, on TinyGo as well.

## Costs

Measured on darwin/arm64 against the same server without it: **305 KB**, of
which `compress/flate` is **81 KB**, linked even when compression is never
enabled. Dropping flate behind a build tag was considered and rejected: the
saving is small next to the complexity, unlike the zstd tag in `../fasthttp`.

Echo throughput, TinyGo against host Go: 33.8k vs 38.0k msg/s at 64 B, 23.0k vs
47.1k at 1 KiB, 3.3k vs 4.2k at 64 KiB.

## Re-vendoring

```bash
go mod download github.com/gorilla/websocket@v1.5.3
python3 websocket/vendor.py
```

`vendor.py` applies every patch as an exact-text replacement with an expected
occurrence count, so a version bump whose anchors moved aborts rather than
silently dropping a patch. Keep it in sync with PATCHES.md.

## Tests

```bash
go test ./websocket                            # std path
go test -tags force_tinygo_logic ./websocket   # TinyGo path, host toolchain
tinygo test ./websocket                        # TinyGo path, real compiler
```

## License

This directory is upstream's BSD-2-Clause; see [LICENSE](./LICENSE) and
[AUTHORS](./AUTHORS). The rest of the repository is Apache-2.0.
