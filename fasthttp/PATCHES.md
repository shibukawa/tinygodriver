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

- Both `CompressHandlerLevel` and `CompressHandlerBrotliLevel` gate their zstd
  case on `zstdAvailable`; see section 6.
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

## 6. Optional zstd

`-tags fasthttp_nozstd` drops `klauspost/compress/zstd`. This is the only patch
here that exists for binary size rather than for TinyGo compatibility, and the
numbers are why — measured standalone under TinyGo on darwin/arm64:

| dependency | added to the binary |
|---|---|
| klauspost flate + gzip + zlib | 0.07 MB |
| brotli | 0.24 MB |
| **zstd** | **2.40 MB** |

zstd alone is most of what fasthttp costs over `net/http`, and roughly ten times
brotli. So zstd is separable and the others are not worth the seam.

- `zstd.go` gains `//go:build !fasthttp_nozstd`, a `zstdAvailable` constant and a
  `zstdReader` alias for `zstd.Decoder`.
- `zstd_disabled.go`, hand-written, replaces it under the tag. The exported API
  stays whole so application code compiles either way.
- `fs.go` drops the `zstd` import, names the decoder through `zstdReader`, and
  derives `compressZstd` as `fs.CompressZstd && zstdAvailable`. Ignoring the
  field rather than honouring it is deliberate: a handler that advertised an
  encoding it cannot produce would break clients, and there is no error return
  on that path to report it through.
- `server.go` gates both `CompressHandler` switches, so a client offering only
  zstd is served identity.

Only the exported *compression* entry points panic — `AppendZstdBytes` and
`AppendZstdBytesLevel`, whose signatures return `[]byte` with no error. Handing
back `dst` unchanged there would produce an empty body for a caller to label
`Content-Encoding: zstd`, and silence is the one thing worse than a panic.
Everything with an `error` in its signature returns `ErrZstdUnsupported`, and
nothing inside fasthttp reaches any of it.

### Why not the repository's own `compress/zstd`

Substituting `compress/zstd`, whose bounded TinyGo encoder is 0.08 MB, looks
like the obvious alternative. It does not work here, and the reason is in that
package's own stated exclusions.

Its bounded encoder emits **one LZ match per block** and stores every other byte
as a raw literal, so its output is the input length minus the single longest
match. That is a consequence of encoding the sequence tables in RLE mode, which
fixes one literal-length, match-length and offset code for the whole block and
therefore admits only one distinct sequence. Measured against klauspost on
payloads a web server actually sends:

| payload | klauspost | bounded encoder | gzip |
|---|---|---|---|
| HTML listing, 14 KB | 5.9% | **99.9%** | 11.7% |
| JSON array, 11 KB | 10.1% | **99.9%** | 13.5% |
| varied text, 5 KB | 27.8% | **100.0%** | 26.9% |
| one repeated string | 1.4% | 1.4% | 2.5% |

The HTML row is the mechanism in miniature: 200 near-identical lines, whose
longest single match is one line, so 14146 bytes become 14125. On dynamic HTML
and JSON it achieves nothing, which would mean spending a zstd frame and the
client's decode to transfer the same number of bytes — strictly worse than the
gzip fasthttp would otherwise have negotiated.

It also excludes decoding outright, so `BodyUnzstd`, `AppendUnzstdBytes` and
FS's pre-compressed `.zst` reading have nothing to call, and it offers neither
`Reset` nor compression levels, which are what fasthttp's per-level encoder
pools are built on.

Dropping zstd and letting negotiation fall through to brotli or gzip saves the
same 2.4 MB and puts *fewer* bytes on the wire than substituting would.
