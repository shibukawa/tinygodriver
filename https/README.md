# https — net/http-compatible HTTPS client for TinyGo

TinyGo ships a stub `crypto/tls`, so `net/http` cannot reach `https` URLs. This
package performs TLS through the TLS stack the host OS already provides and
exposes the familiar `net/http` surface.

```go
package main

import (
	"fmt"
	"io"

	"github.com/shibukawa/tinygodriver/https"
)

func main() {
	resp, err := https.Get("https://example.com/")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.Status, len(body))
}
```

Request and response types are the standard `net/http` types, so replacing
`http.Get` with `https.Get` is usually the only change an application needs.

## API

| Function | Mirrors |
|---|---|
| `Get(url)` | `http.Get` |
| `Head(url)` | `http.Head` |
| `Post(url, contentType, body)` | `http.Post` |
| `PostForm(url, data)` | `http.PostForm` |
| `NewClient(opts...) *http.Client` | — |
| `NewTransport(opts...) *Transport` | — |

`Transport` implements `http.RoundTripper`, so it drops into an `http.Client`:

```go
client := &http.Client{
	Transport: https.NewTransport(https.WithRootCAFile("ca.pem")),
	Timeout:   10 * time.Second,
}
```

## Configuration

`Config` is deliberately **not** `crypto/tls.Config`: TinyGo builds must not
link `crypto/tls`. It takes PEM bytes, the one representation every backend
accepts.

```go
client := https.NewClient(
	https.WithRootCAFile("/etc/ssl/private-ca.pem"),
	https.WithMinVersion(https.VersionTLS13),
)
```

| Option | Effect |
|---|---|
| `WithRootCAPEM(pem)` | add PEM trust anchors |
| `WithRootCAFile(path)` | add anchors from a PEM file |
| `WithRootCAsOnly(bool)` | ignore the system trust store |
| `WithClientCertificate(cert, key)` | client certificate for mTLS |
| `WithInsecureSkipVerify(bool)` | disable verification (testing only) |
| `WithServerName(name)` | override SNI and the verified name |
| `WithMinVersion(v)` | minimum TLS version, default TLS 1.2 |

The zero value verifies the peer chain and hostname against the system trust
store and requires TLS 1.2 or later. Custom CAs are **added** to the system
anchors unless `WithRootCAsOnly(true)` is set. No build ever falls back to
plaintext or to an unverified connection.

## Connection reuse

Connections are kept and reused between requests to the same destination, the
same way `net/http` does it, so the TLS handshake is paid per connection rather
than per request. Against a real AWS service endpoint from Tokyo, six sequential
`POST`s under TinyGo measured 89–105 ms each without reuse and 10–12 ms each
with it; the remainder is the round trip itself.

`Transport` carries the knobs, and they mean the same thing on both build paths:

| Field | Default | Effect |
|---|---|---|
| `MaxIdleConnsPerHost` | 2 | idle connections kept per destination |
| `IdleConnTimeout` | 20s | how long an idle connection stays reusable |
| `DisableKeepAlives` | false | close after every request |

`CloseIdleConnections()` drops the pooled connections without touching requests
in flight.

`MaxIdleConnsPerHost` is the whole pool when every request goes to one host,
which is what talking to a single service endpoint looks like. Four concurrent
requests against the default of 2 measured two reused connections at 15–17 ms
and two fresh handshakes at 94–97 ms. Set it to the concurrency you actually
run.

The default `IdleConnTimeout` is far below `net/http`'s 90 seconds on purpose.
`net/http` can cheaply notice that a pooled connection died; the native
backends cannot, so a stale entry costs a retry instead. Keep it under the
server's own idle timeout.

That retry puts the request on the wire a second time. It is gated on no
response byte having arrived and on a body `net/http` can rebuild, but a
request that must not be delivered twice still can be, because the server may
have acted before the connection died. Only a pooled connection is ever
replayed, so `DisableKeepAlives` — at the cost of a handshake per request — is
what removes the possibility.

TLS session resumption is **not** used. Forcing it on and off on the macOS dial
path both measured identical to the default, and Network.framework's session
cache is process-wide while verification varies per `Config`, so opting into it
would trade a measured zero for a real risk.

## Proxies

The proxy environment is read on every build, with the same variables and the
same precedence `net/http` uses, so a program behaves identically whichever
compiler produced it:

```bash
export HTTPS_PROXY=http://proxy.example.com:8080
export NO_PROXY=internal.example.com,.corp.example.com
```

`HTTPS_PROXY` selects the proxy for `https://` requests and `HTTP_PROXY` for
`http://` ones; the lowercase spellings are accepted as fallbacks. Credentials
in the URL become a `Proxy-Authorization` header.

Note that the scheme in the variable describes the hop **to the proxy**, not to
the origin. With `HTTPS_PROXY=http://…` the `CONNECT` request is plaintext, and
the TLS session inside the tunnel is still end to end with the origin — the
proxy sees only the host name and port. The certificate is verified against the
origin, never the proxy.

Standard Go builds get this from `net/http`. Native builds do it here, using
`DialPlain` and `Upgrade`: dial the proxy, write `CONNECT`, then start TLS on
that same socket. On macOS the proxied path therefore runs through Secure
Transport rather than Network.framework, so it caps at **TLS 1.2**, the same
trade the STARTTLS path documents below.

## Errors

Native status codes map onto sentinels, so application code branches the same
way on every platform:

```go
if errors.Is(err, https.ErrUntrustedRoot) { ... }
```

`ErrHandshakeFailed`, `ErrCertificateInvalid`, `ErrCertificateExpired`,
`ErrHostnameMismatch`, `ErrUntrustedRoot`, `ErrClientCertificateRejected`,
`ErrProtocolVersion`, `ErrPlatformNotSupported`.

Errors are wrapped in `*https.Error`, which also carries the raw native status
code for diagnosis.

## Implementation selection

| Build | Backend | Max TLS | STARTTLS | Client certs |
|---|---|---|---|---|
| standard Go, any OS | `net/http` + `crypto/tls` | 1.3 | n/a | yes |
| TinyGo on macOS | Network.framework + Secure Transport | 1.3 dial, **1.2 upgrade** | yes | no |
| TinyGo on macOS, `-tags darwinstarttlswith13` | vendored mbedTLS | 1.3 | yes | yes |
| TinyGo on Linux | vendored mbedTLS | 1.3 | yes | yes |
| TinyGo on Windows | Schannel | 1.3 where the OS offers it, else 1.2 | yes | RSA keys only |
| other TinyGo targets | `ErrPlatformNotSupported` | — | — | — |
| any, `-tags force_tinygo_logic` | forces the native backend on host Go | | | |

`force_tinygo_logic` makes the native C code testable without a TinyGo
toolchain:

```bash
go test -tags force_tinygo_logic ./https
go test -tags "force_tinygo_logic darwinstarttlswith13" ./https
```

## Build tag tradeoffs

There is exactly one choice to make, and only on macOS.

### Default: Network.framework for dialing, Secure Transport for upgrading

macOS ships two usable TLS stacks and neither covers everything, so this build
carries both and picks per operation.

`nw_connection` owns DNS, TCP and TLS as one unit. That is why it reaches TLS
1.3, and equally why it cannot start TLS on a socket that has already carried
plaintext. Secure Transport is the opposite: a byte transformer with
caller-supplied I/O, so it can adopt any socket at any point, but Apple never
gave it TLS 1.3.

| | |
|---|---|
| **Gain** | TLS 1.3 for ordinary `https.Get`, small binary, both stacks ship in the OS |
| **Cost** | in-band upgrades cap at **TLS 1.2**; no client certificates; depends on an API Apple marks deprecated |

The version asymmetry is deliberate and visible: `WithMinVersion(VersionTLS13)`
on the upgrade path returns `ErrProtocolVersion` rather than quietly
negotiating something weaker.

### `-tags darwinstarttlswith13`: mbedTLS for everything

Replaces both with the same vendored mbedTLS the Linux build uses, so macOS
behaves exactly like Linux.

| | |
|---|---|
| **Gain** | TLS 1.3 on **both** paths, client certificates work, one code path and one error mapping shared with Linux, no deprecated API |
| **Cost** | binary grows from about 1.2 MB to 1.7 MB, and — the real cost — macOS trust **policy** is no longer applied |

That last point deserves more than a line. The default build hands verification
to the OS, so admin or MDM distrust settings, enterprise roots and Apple's own
evaluation policy all apply. mbedTLS instead validates the chain itself against
`/etc/ssl/cert.pem`, a static snapshot refreshed only by OS updates. A root an
administrator has explicitly distrusted is still in that file. On Linux there
was no OS policy to give up, which is why the same tradeoff was easy there and
is not here.

Choose this tag when TLS 1.3 on an upgraded connection, or client
certificates, matter more than OS trust policy.

## Platform notes

### macOS

Both default backends ship in the OS. The C bridges hand-declare the `nw_*`,
`sec_*`, `SSL*`, `CF*` and `dispatch_*` symbols they use rather than including
framework headers, because TinyGo compiles cgo C with `-nostdlibinc` and
rejects `-F`/`-iframework` in `CFLAGS`. That also decouples the build from the
installed SDK version.

Building with TinyGo needs either Xcode or the Command Line Tools at their
standard location; both paths are listed in the link flags and the linker
ignores whichever is absent. TinyGo honors neither `CGO_CFLAGS` nor
`CGO_LDFLAGS`, so these cannot be overridden from the environment.

Trust evaluation uses the system keychain on both default backends. Custom
anchors go through `SecTrustSetAnchorCertificates`, reached via a verify block
on Network.framework and via `kSSLSessionOptionBreakOnServerAuth` on Secure
Transport.

### Linux

Uses **mbedTLS, vendored and compiled from source** rather than the system
OpenSSL. TinyGo cannot link the OS OpenSSL at all: it emits no `PT_INTERP`, so
dynamic relocations are never applied and the first `libcrypto` call jumps to
address 0, while a static link needs roughly forty shim symbols whose set
depends on how the distribution built OpenSSL. Compiling mbedTLS with TinyGo's
own cgo avoids both, because the result is built against TinyGo's musl and
linked statically. The target therefore needs **no TLS library installed**.

The cost is that this repository ships a TLS implementation and must track
mbedTLS advisories; see `internal/mbedtls/PATCHES.md`. Binaries are also
larger, and AES/SHA hardware acceleration is enabled because the software
fallback is roughly 27x slower for AES-GCM.

mbedTLS has no system trust store, so the CA bundle is read in Go from
`$SSL_CERT_FILE`, `$SSL_CERT_DIR`, or the usual distro locations. If none is
found, dialing fails with an error naming the paths searched rather than
silently trusting nothing.

### Windows

Uses **Schannel**, reached through SSPI. `secur32.dll`, `crypt32.dll` and
`ncrypt.dll` ship with the OS, so like macOS there is nothing to install.

Windows is the one platform where dialing and upgrading are the same code path.
SSPI is a buffer transformer: it produces and consumes token bytes and never
learns that a socket exists, so a socket that has already carried plaintext is
no different to it from a fresh one. STARTTLS therefore needs no second backend
the way macOS does, and it keeps whatever version the OS negotiates.

The credential is acquired with `SCH_CREDENTIALS` first, which is what lets
Schannel negotiate **TLS 1.3** on Windows 11 and Server 2022. Implementations
that reject that structure — older Windows, and Wine — fall back to the
deprecated `SCHANNEL_CRED` and cap at TLS 1.2.

Verification is taken over from Schannel with
`SCH_CRED_MANUAL_CRED_VALIDATION` and redone through `CertGetCertificateChain`
plus `CertVerifyCertificateChainPolicy`, so custom anchors, `RootCAsOnly` and
`InsecureSkipVerify` behave exactly as they do on the other backends. Extra
anchors live in an in-memory `HCERTSTORE`; the user's certificate store is
never written to. `RootCAsOnly` uses a chain engine with `hExclusiveRoot`,
which is the only way Windows will treat a supplied root as the *only* root.

Unlike the darwin bridges this one includes the system headers. SSPI and
crypt32 structures are far too intricate to redeclare by hand safely, and a
silently wrong layout would corrupt memory on the platform this repository is
least able to run. The exceptions are the credential structures and
`CERT_CHAIN_ENGINE_CONFIG`, which are redeclared under private names because
their presence varies across mingw-w64 releases.

Running the tests on Windows takes some care, because a plain `go test ./...`
does **not** reach this backend at all — without `force_tinygo_logic` the
package compiles `roundtrip_std.go` and delegates to `crypto/tls`, so a green
run says nothing about Schannel:

```bash
go test -tags force_tinygo_logic ./...
```

That needs `CGO_ENABLED=1` and a C compiler on `PATH`, because Schannel is
reached through cgo. Go on Windows silently sets `CGO_ENABLED=0` when it finds
no compiler; in that case the backend reports `ErrPlatformNotSupported` rather
than failing to build, and `https` without the tag still works because it
delegates to `crypto/tls`.

`netdev` is the exception to the tag rule: its `IPPROTO_TLS` support is not
behind `force_tinygo_logic`, so `TestIPProtoTLS` exercises Schannel under a
plain `go test ./netdev` too — when cgo is available.

Note that the design notes originally called for a pure-Go binding through
`syscall.NewLazyDLL`. TinyGo ships no Windows `syscall` implementation at all,
so that is not available; SSPI is reached the same way `netdev/sys_windows.go`
already reaches winsock, through cgo. Building for Windows therefore needs a
mingw-w64 toolchain, which that file already required.

### Known limitations

- **An `https://` proxy is refused.** Talking TLS *to* the proxy and then
  running the origin's TLS inside that tunnel means TLS in TLS, and every
  native backend starts from a descriptor, so the inner session has no socket
  to use. `ErrProxyScheme` is returned rather than quietly connecting direct,
  which would send traffic a deployment expects to be proxied. `socks5://` is
  refused for the same reason. An `http://` proxy — by far the common case —
  works.
- **`NO_PROXY` does not accept CIDR ranges.** Host names, domain suffixes,
  literal IP addresses and an optional port all work; `10.0.0.0/8` does not.
- **Client certificates are not supported on macOS.** Network.framework needs a
  `SecIdentityRef`, which requires importing the private key into a keychain.
  `WithClientCertificate` returns `ErrClientCertificateUnsupported` there. They
  work in standard Go builds and on Linux.
- **Client certificates on Windows must carry an RSA key.** Schannel wants a
  `CERT_CONTEXT` with a CNG key handle, and the only blob `CryptDecodeObjectEx`
  hands straight to `NCryptImportKey` is RSA. Both `RSA PRIVATE KEY` (PKCS#1)
  and `PRIVATE KEY` (PKCS#8 wrapping RSA) are accepted; an EC key returns
  `ErrClientCertificateUnsupported` rather than being silently dropped.
- **The Windows backend has never run on Windows.** It cross-compiles, vets and
  links under mingw-w64, and 10 of the 21 tests pass under Wine 11 — including
  the handshake, record I/O, the STARTTLS upgrade, and every rejection path.
  The other 11 fail on two Wine stubs rather than on this code: Wine's crypt32
  accepts only the pre-Vista `CERT_CHAIN_ENGINE_CONFIG` and so cannot do
  `hExclusiveRoot` verification, and its CNG is a stub that cannot import a
  private key. Custom-anchor acceptance and client certificates therefore need
  a real Windows host. See `requirement:windows-tinygo-feasibility` for the
  measured detail.
- **Certificate failure reasons are coarse when a custom CA is configured.** The
  verify block reports a single accept/reject decision, so a hostname mismatch
  and an untrusted chain can both surface as `ErrCertificateInvalid`. Without a
  custom CA the framework's own status codes are used and are more specific.
- **`http.Client.Timeout` is ignored under TinyGo.** TinyGo's `net/http` drops
  the `setRequestCancel` machinery that carries it to a custom `RoundTripper`,
  so the transport never learns about it. Use a request context deadline, or
  `Transport.DialTimeout` / `Transport.ResponseTimeout`, which work everywhere.
- **A stale pooled connection costs one retry.** Connections are reused across
  requests, but no native backend can report that a peer closed an idle one
  without reading from it, and a background reaper would deadlock the netdev
  scheduler. A connection that turns out to be dead is therefore recovered by
  resending the request, which needs a body `net/http` can rebuild — that is
  automatic for the readers `http.NewRequest` recognises, and absent for an
  arbitrary `io.Reader`. Keep `Transport.IdleConnTimeout` (20s by default)
  below the server's own idle timeout, or set `DisableKeepAlives` to go back to
  one connection per request.
- **HTTP/1.1 only.** No ALPN negotiation to h2.
- Server-side TLS is not provided.
