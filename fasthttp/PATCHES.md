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

Two reasons, and they are unrelated. One is `crypto/tls`, below. The other is
that upstream's zstd does not link under TinyGo at all — section 6, and the only
one of the two that stops a build outright.

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
`tinygo` alone. `force_tinygo_logic` is deliberately not offered *there*: every
divergence in that pair is a missing standard-library symbol rather than
alternate logic, so a host-Go build has nothing to exercise, and
`cloneTLSConfig` would copy the mutex inside a real `tls.Config`. The zstd files
do take the tag, because they hold real alternate logic; see section 6.

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
  case on `zstdAvailable`; see sections 6 and 7.
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

## 6. zstd on TinyGo

This is the one patch that is not about `crypto/tls`, and it is the one that
decides whether `tinygo build` works at all. `klauspost/compress/zstd` decodes
in hand-written assembly, and TinyGo links none of it:

```
fse_decoder_asm.go:45: linker could not find symbol …zstd.buildDtable_asm
seqdec_arm64.go:25:    linker could not find symbol …zstd.sequenceDecs_decode_56_arm64
… four more
```

It fails at *link* time, after a clean compile, which is a miserable way to
find out. `-tags noasm` gets past it by selecting klauspost's pure-Go fallbacks
and costs 2.49 MB.

So TinyGo encodes through this repository's own
[`compress/zstd`](../compress/zstd) instead, which is pure Go:

| build of `examples/fasthttpserver` | size |
|---|---|
| klauspost, `-tags noasm` | 5.82 MB |
| **compress/zstd, no tags** | **3.33 MB** |
| no zstd, `-tags fasthttp_nozstd` | 3.28 MB |

zstd went from 2.49 MB to 0.05 MB, and `noasm` now changes nothing because no
TinyGo build reaches klauspost's zstd at all.

The seam follows the repository's `rule:build-tag-selection`:

| file | constraint | what it can do |
|---|---|---|
| `zstd.go` | `!fasthttp_nozstd && !tinygo && !force_tinygo_logic` | encode and decode, klauspost, upstream's own code |
| `zstd_tinygo.go` | `!fasthttp_nozstd && (tinygo \|\| force_tinygo_logic)` | encode only |
| `zstd_disabled.go` | `fasthttp_nozstd` | neither |

`force_tinygo_logic` selects the TinyGo half on standard Go, which is how the
encoder is tested against a decoder at all — `zstd_wire_test.go` reads back with
klauspost what `compress/zstd` put on the wire. This is the first divergence in
this package that is alternate *logic* rather than a missing standard-library
symbol, which is why it takes the tag and `compat_std.go` still does not.

### What TinyGo gives up

**Decoding.** `compress/zstd` excludes it. `BodyUnzstd`, `WriteUnzstd` and
`AppendUnzstdBytes` report `ErrZstdUnsupported`, so a *client* under TinyGo
cannot read a zstd response — send `Accept-Encoding: br, gzip` and nothing is
lost, since a server that offers zstd offers those too. A program that really
needs to decode can call `klauspost/compress/zstd` itself on `resp.Body()`
under `-tags noasm`; nothing here stops it.

**Compression levels.** `compress/zstd` has one. The `CompressZstd*` constants
and the `level` parameters stay, and are normalized and then ignored, so
application code compiles and behaves the same either way. The per-level pool
maps collapse to one pool per writer kind.

**FS zstd.** `compressZstd` is derived as `fs.CompressZstd &&
zstdDecodeAvailable`, so `FSHandler` serves br and gzip on TinyGo and never
zstd. It is gated on the *decoder* because `newFSFile` reads a compressed file
back to sniff its content type when the extension yields no MIME type — an
encode-only build would answer with `application/octet-stream` for the zstd
representation of a file and a sniffed type for the identity one, and a
`Content-Type` that varies with `Accept-Encoding` is worse than no zstd. If
`compress/zstd` ever grows a decoder, flipping `zstdDecodeAvailable` turns this
back on and nothing else changes.

What TinyGo keeps is the case that matters most: `CompressHandler` and
`CompressHandlerBrotliLevel` negotiate zstd for dynamic responses, and
`Response.zstdBody` and `SetBodyStreamWriter` compress through the pooled
encoder.

### `Reset`, added upstream of here

`compress/zstd` gained `(*Writer).Reset(io.Writer)` for this. fasthttp pools its
encoders and resets them onto the next response; without it every response would
build a new 128 KiB block buffer and 16 KiB match table, which is most of what
the encoder weighs. Both implementations in that package have it, and it keeps
the ETag setting.

## 7. Optional zstd

`-tags fasthttp_nozstd` drops zstd altogether. It predates section 6, when zstd
was 2.40 MB of TinyGo binary against brotli's 0.24 MB and flate + gzip + zlib's
0.07 MB, and it saved half the binary. It now saves 0.05 MB, and remains for
programs that will never negotiate zstd at all.

- `zstd_disabled.go`, hand-written, replaces both other halves under the tag.
  The exported API stays whole so application code compiles either way.
- `fs.go` drops the `zstd` import and names the decoder through `zstdReader`,
  which is an alias for `zstd.Decoder` on standard Go and a stub in the other two
  builds.
- `server.go` gates both `CompressHandler` switches on `zstdAvailable`, so a
  client offering only zstd is served identity.

Only the exported *compression* entry points panic — `AppendZstdBytes` and
`AppendZstdBytesLevel`, whose signatures return `[]byte` with no error. Handing
back `dst` unchanged there would produce an empty body for a caller to label
`Content-Encoding: zstd`, and silence is the one thing worse than a panic.
Everything with an `error` in its signature returns `ErrZstdUnsupported`, and
nothing inside fasthttp reaches any of it.

`ErrZstdUnsupported` is declared in all three files, including the standard-Go
one that never returns it, so that `errors.Is` against it compiles under every
tag combination.

## 8. `sync.RWMutex` becomes `internal/syncx.RWMutex`

TinyGo's `sync.RWMutex` (through at least 0.42) deadlocks whenever a reader
arrives while a writer is waiting for the existing readers to drain; see
`internal/syncx` for the mechanism and upstream `tinygo-org/tinygo#5630` for
the fix. `HostClient.mLock` is exactly that shape — every request read-locks
it and the first request to a new host write-locks it — so it is swapped for
the shim, which is `sync.RWMutex` on standard Go and a plain mutex on TinyGo.

`lbclient.go` and the two `fasthttputil` files follow so that the policy test
in `internal/syncx` (`TestNoStdRWMutexOnTinyGoPath`) stays green; their
writers are rare, but a plain mutex costs nothing around a field read. Each
site is a one-line type change plus the import, appended as its own group at
the end of the block so gofmt leaves it in place.
