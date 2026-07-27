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

### Known limitations

- **Client certificates are not supported on macOS.** Network.framework needs a
  `SecIdentityRef`, which requires importing the private key into a keychain.
  `WithClientCertificate` returns `ErrClientCertificateUnsupported` there. They
  work in standard Go builds and on Linux.
- **Certificate failure reasons are coarse when a custom CA is configured.** The
  verify block reports a single accept/reject decision, so a hostname mismatch
  and an untrusted chain can both surface as `ErrCertificateInvalid`. Without a
  custom CA the framework's own status codes are used and are more specific.
- **`http.Client.Timeout` is ignored under TinyGo.** TinyGo's `net/http` drops
  the `setRequestCancel` machinery that carries it to a custom `RoundTripper`,
  so the transport never learns about it. Use a request context deadline, or
  `Transport.DialTimeout` / `Transport.ResponseTimeout`, which work everywhere.
- **No connection reuse.** Each request opens and closes one TLS connection.
- On Linux, `go test -tags force_tinygo_logic` also links `netdev`, which uses
  OpenSSL on host-Go builds, so that test run needs `libssl-dev`. TinyGo builds
  do not.
- **HTTP/1.1 only.** No ALPN negotiation to h2.
- Server-side TLS is not provided.
