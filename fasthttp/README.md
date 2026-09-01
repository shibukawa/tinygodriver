# fasthttp

A fork of [valyala/fasthttp](https://github.com/valyala/fasthttp) v1.73.0 that
builds under TinyGo. It is a drop-in replacement: change the import path and
nothing else.

```go
import "github.com/shibukawa/tinygodriver/fasthttp"
```

The same source builds on both compilers, so application code and tests are
shared. On standard Go this is upstream fasthttp, behaviour for behaviour — the
divergences are all on the TinyGo side, and [PATCHES.md](./PATCHES.md) lists
every one.

There is no wrapper package selecting between this fork and upstream, unlike
`database/sql/mysql` and `database/pgx`. Those wrap a `database/sql` driver,
whose whole surface is nine interfaces; an application using fasthttp names
`Server`, `RequestCtx`, `Request`, `Response`, `Args`, `URI` and a few hundred
methods directly, and a hand-maintained re-export layer over that would break on
every upstream release.

## Licence

**This directory is MIT**, not the Apache 2.0 of the rest of the repository.
`LICENSE` is upstream's own. Every change to upstream source carries a
`PETITWEB:` comment so a diff against v1.73.0 stays readable.

## Quick start

Blank-import `netdev` to give TinyGo's `net` a socket layer, exactly as with
`net/http`. On standard Go that import is a no-op.

```go
package main

import (
	"fmt"

	"github.com/shibukawa/tinygodriver/fasthttp"
	_ "github.com/shibukawa/tinygodriver/netdev"
)

func main() {
	fasthttp.ListenAndServe(":8080", func(ctx *fasthttp.RequestCtx) {
		fmt.Fprintf(ctx, "hello from %s\n", ctx.RemoteIP())
	})
}
```

No build tags are needed on either compiler:

```bash
tinygo build -o server ./yourapp
go build ./yourapp
```

`-tags noasm` used to be required and is now inert; see below.

## zstd

Upstream fasthttp cannot be linked by TinyGo at all, because
`klauspost/compress/zstd` decodes in hand-written assembly and TinyGo links
none of it. The failure comes at link time, after a clean compile:

```
seqdec_arm64.go:25: linker could not find symbol …zstd.sequenceDecs_decode_56_arm64
```

Under TinyGo this fork encodes through the repository's own
[`compress/zstd`](../compress/zstd) instead, which is pure Go. That is what
makes a plain `tinygo build` work, and it takes 2.49 MB out of the binary:

| minimal two-route server, TinyGo | size |
|---|---|
| `net/http` | 1.22 MB |
| **this fork, no tags** | **2.83 MB** |
| this fork, `-tags fasthttp_nozstd` | 2.78 MB |
| this fork before, `-tags noasm` | 5.32 MB |

zstd costs 0.05 MB now, against 2.53 MB through klauspost. brotli is 0.24 MB and
gzip, deflate and zlib together 0.07 MB; neither is worth a seam.

Standard Go is unchanged — klauspost, upstream's own code, encoding and decoding
and all seven compression levels.

**What TinyGo gives up is decoding.** `compress/zstd` is an encoder.
`BodyUnzstd`, `WriteUnzstd` and `AppendUnzstdBytes` report
`ErrZstdUnsupported`, so a *client* under TinyGo should ask for
`Accept-Encoding: br, gzip`, which loses nothing in practice: a server that
offers zstd offers those too. A program that must decode can call
`klauspost/compress/zstd` on `resp.Body()` itself under `-tags noasm`.

Two smaller consequences:

- **Compression levels do nothing.** `compress/zstd` has one. The
  `CompressZstd*` constants and every `level` parameter stay, so application
  code compiles and behaves the same either way.
- **`FSHandler` does not serve zstd**, and `FS.CompressZstd` is ignored, because
  serving a `.zst` cache entry means reading it back to sniff a content type.
  Static files get br and gzip. Dynamic responses through `CompressHandler`,
  `CompressHandlerBrotliLevel` and `SetBodyStreamWriter` do get zstd — that is
  the case worth having.

## Dropping zstd entirely

`-tags fasthttp_nozstd` leaves zstd out on either compiler:

```bash
tinygo build -tags fasthttp_nozstd -o server ./yourapp
```

It saved half the binary when zstd meant klauspost; it now saves 0.05 MB, and is
worth reaching for only in a program that will never negotiate zstd.

A client that offers only `zstd` is then served identity, and one that also
offers `br` or `gzip` gets those, so content negotiation does the right thing
without any change to your handlers. `BodyUnzstd` and friends report
`ErrZstdUnsupported`; `AppendZstdBytes` and `AppendZstdBytesLevel` panic, because
their signatures cannot return an error and quietly handing back an empty body
would be worse. Nothing inside fasthttp calls any of them in this build.

## What works

Verified under TinyGo 0.42.0 on darwin/arm64, against a suite that runs
identically on both compilers:

- Server and client round trips, every method, keep-alive, chunked
  `SetBodyStreamWriter`, `ServeConn` on any `net.Conn`
- 4 MiB request and response bodies; headers including repeated ones; cookies
- `multipart/form-data` uploads
- gzip, deflate and br compressed and decompressed; zstd compressed, and read
  back with klauspost under `-tags force_tinygo_logic` to prove the frame is
  real
- `FSHandler`, including range requests
- Redirect following, `DoTimeout`, `RemoteIP`, graceful `Shutdown`
- 3200 concurrent requests with no errors, at roughly 40% of standard Go's
  throughput on the same machine

## What does not work

### Serving TLS is impossible

`ServeTLS`, `ServeTLSEmbed` and `AppendCertEmbed` report `ErrTLSUnsupported`.
Terminating TLS needs `tls.Server` and `tls.X509KeyPair`, and TinyGo defines
neither. Put a terminator in front — nginx, Caddy, or a cloud load balancer.

The fork refuses rather than degrading, which is the point: TinyGo's
`tls.NewListener` returns a listener that performs no handshake, so upstream's
code path would have served **cleartext on port 443** without a word.

### HTTP/2 is impossible

`Server.NextProto` can never fire. ALPN selection is reported through
`tls.ConnectionState.NegotiatedProtocol`, and TinyGo's `ConnectionState` has no
such field, so the negotiated protocol never reaches the process.

### An HTTPS client needs its own Dial

`Client` and `HostClient` cannot originate TLS on their own — `tls.Client` panics
under TinyGo. Supply a `Dial` that returns an already-encrypted connection and
it works, because `dialAddr` treats any connection with a `Handshake() error`
method as one that has been through TLS already:

```go
hc := &fasthttp.HostClient{
	Addr:  "example.com:443",
	IsTLS: true,
	Dial:  func(addr string) (net.Conn, error) { return net.DialTLS(addr) },
}
```

`net.DialTLS` goes through netdev's `IPPROTO_TLS` socket: Secure Transport on
macOS, Schannel on Windows, `crypto/tls` on host-Go Linux. TinyGo on Linux
reports `ErrProtocolNotSupported`, so use a `Dial` built on
[`https`](../https) there instead. Connection reuse works either way — measured
478 ms for the handshake and 12 ms for the next request on the pooled connection.

## Things that behave differently

- **Name resolution belongs to the dialer.** `TCPDialer`'s DNS cache and its
  round-robin over multiple A records go unused, because netdev resolves inside
  `Connect`. Setting `TCPDialer.Resolver` explicitly overrides this.
- **`DialTimeout` does not bound the dial itself**, and `TCPDialer.LocalAddr` has
  no effect. Both come from TinyGo's `net.Dialer.DialContext`, which ignores its
  context and its local address.
- **File serving is not zero-copy.** `copyZeroAlloc`'s sendfile paths need
  `ReadFrom` on `os.File` and `net.TCPConn`, which TinyGo does not implement, so
  bodies copy through a pooled buffer.
- **Binding to port 0 hides the port.** `net.Listener.Addr()` reports the address
  it was asked for, so a server on `:0` cannot discover where it landed. Pick a
  port.
- **Pooled memory is never released.** TinyGo's `sync.Pool` is one
  mutex-guarded slice with no eviction, so fasthttp's pooled contexts and buffers
  settle at peak concurrency and stay there. That single lock is also contended,
  which is most of the throughput gap.

## Binary size

Under standard Go the two are the same size; under TinyGo, whose whole-program
optimisation rewards `net/http`'s smaller dependency graph, fasthttp costs more:

| Program | TinyGo | standard Go |
|---|---|---|
| Two routes on `net/http` | 1.22 MB | 7.72 MB |
| Two routes on this fork | 2.83 MB | 8.18 MB |
| Two routes on this fork, `-tags fasthttp_nozstd` | 2.78 MB | 7.78 MB |

The two compilers pay for zstd in opposite proportions: 0.05 MB of TinyGo
binary through `compress/zstd`, 0.40 MB of standard-Go binary through
klauspost. Before the swap the TinyGo row read 5.32 MB; see the zstd section
above.

## Updating

```bash
python3 vendor.py                     # from the module cache
python3 vendor.py /path/to/src        # from an unpacked tree
```

Bump `FASTHTTP_VERSION` in `vendor.py` first. Each patch carries the number of
occurrences it expects, so a moved anchor aborts the run rather than half
applying; reconcile it against [PATCHES.md](./PATCHES.md) and re-run.
