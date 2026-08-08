# Local patches to the vendored fasthttp

Vendored by `vendor.py` from `github.com/valyala/fasthttp v1.73.0`.

Every change below is applied by `vendor.py` itself, as an exact-text
replacement with an expected occurrence count. A count that no longer matches
aborts the run, so a version bump fails loudly instead of silently dropping a
patch. Keep this file and that script in sync.

Upstream is otherwise untouched — 50 sources, import paths rewritten only, and
every edit marked with a `PETITWEB:` comment so a diff against v1.73.0 stays
readable.

## Why any patch is needed

TinyGo ships `crypto/tls` as a stub. Ten compile errors across seven sites, and
not one of them is a design conflict — every one is a symbol that upstream uses
and TinyGo does not define:

| Missing | Sites |
|---|---|
| `(*tls.Config).Clone` | `client.go`, `server.go` ×2 |
| `tls.Conn`, as a *type* | `peripconn.go` |
| `tls.ConnectionState.NegotiatedProtocol` | `server.go` |
| `tls.X509KeyPair` | `server.go` |
| `(*os.File).ReadFrom`, `(*net.TCPConn).ReadFrom` | `http.go` |
| `net.DefaultResolver` | `tcpdialer.go` |

Two more sites are patched for behaviour rather than to compile: `tls.Client`
compiles but panics, and `tls.NewListener` compiles and returns a listener that
performs no handshake at all.

The replacements live in `compat_std.go` and `compat_tinygo.go`, constrained on
`tinygo` alone. `force_tinygo_logic` is deliberately not offered: every
divergence here is a missing standard-library symbol rather than alternate
logic, so a host-Go build has nothing to exercise, and `cloneTLSConfig` would
copy the mutex inside a real `tls.Config`.

## 1. `client.go`

- `newClientTLSConfig` calls `cloneTLSConfig` instead of `c.Clone()`.
- `tlsClientHandshake` and `dialAddr` obtain their connection from `tlsClient`,
  which reports `ErrTLSUnsupported` on TinyGo. Upstream called `tls.Client`,
  which **panics** there rather than failing — a stub that compiles is worse
  than one that does not, because nothing warns you until a request is in
  flight.

  This is not the end of HTTPS on TinyGo. `dialAddr` treats any connection
  carrying a `Handshake() error` method as one that has already been through
  TLS, and TinyGo's `net.TLSConn` has exactly that method, so a `Dial` returning
  `net.DialTLS` reaches an HTTPS server through the OS TLS stack. That path is
  upstream's own and needed no patch; see the README.

## 2. `server.go`

- `getNextProto` calls `negotiatedProtocol`, which is `""` on TinyGo.
  `ConnectionState` there has no `NegotiatedProtocol` field, so ALPN cannot be
  observed and the handlers registered through `Server.NextProto` — HTTP/2 among
  them — can never run. Nothing can be shimmed here; the information does not
  reach the process.
- `ServeTLS` and `ServeTLSEmbed` clone through `cloneTLSConfig`, and take their
  listener from `newTLSListener` rather than `tls.NewListener`.

  The listener change is the one that matters for safety. TinyGo's
  `tls.NewListener` returns a wrapper that does no handshake, so serving through
  it would put **cleartext on the TLS port** — a silent downgrade of exactly the
  kind `requirement:platform-matrix` forbids. `newTLSListener` reports
  `ErrTLSUnsupported` instead.
- `x509KeyPair`'s body moves into the compat files, because `tls.X509KeyPair`
  does not exist on TinyGo. Only the error wording stays in `server.go`, as
  `errCannotLoadTLSKeyPair`.

`loadX509KeyPair` is untouched: TinyGo does define `tls.LoadX509KeyPair`, as a
stub that returns an error, which is already the behaviour we want.

## 3. `peripconn.go`

`perIPTLSConn` embeds `*tlsConnImpl`, an alias for `tls.Conn` on standard Go and
`net.TLSConn` on TinyGo.

The embedded type has to stay a concrete TLS connection. `perIPTLSConn` exists
as a separate type from `perIPConn` precisely so that `*perIPTLSConn` still
satisfies the `tlsConn` interface in `server.go` through promotion — falling
back to embedding `net.Conn` would compile and then quietly stop `getNextProto`
and `RequestCtx.TLSConnectionState` from recognising a TLS connection.

## 4. `http.go`

`copyZeroAlloc` moves whole into the compat files. Its fast paths hand the copy
to the kernel through `(*os.File).ReadFrom` and `(*net.TCPConn).ReadFrom`, and
TinyGo's versions of those types implement neither `ReadFrom` nor `WriteTo`.
The TinyGo copy keeps the `io.WriterTo` and `io.ReaderFrom` interface checks,
which still match `bytes.Buffer` and friends, and otherwise falls through to the
pooled buffered copy that upstream uses as its own last resort.

Consequence worth stating plainly: file serving through `FSHandler` and
`ServeFile` copies through user space on TinyGo. It is correct, and it is not
zero-copy.

## 5. `tcpdialer.go`

- `resolveTCPAddrs` takes its default from `defaultResolver()`. TinyGo's `net`
  exports no resolver at all — only `LookupPort` — so there is nothing to point
  at.
- `(*TCPDialer).dial` skips resolution when `resolveInDialer` is set and no
  `Resolver` was supplied, handing the address to the dialer whole.

  This mirrors what the vendored pgx does, for the same reason: netdev resolves
  names inside `Connect`, so `net.Dialer.DialContext` already accepts a
  hostname. The cost is that `TCPDialer`'s DNS cache and its round-robin over
  multiple A records go unused on TinyGo; the OS resolver does its own caching.
  A caller that sets `Resolver` explicitly still wins.

Two upstream behaviours are worth knowing about on this path, neither of them
patched, because both live in TinyGo's `net` rather than in fasthttp:
`Dialer.DialContext` ignores its context, so `DialTimeout` does not bound the
dial itself, and it ignores `Dialer.LocalAddr`, so `TCPDialer.LocalAddr` has no
effect.
