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

| Build | Backend | Client certs |
|---|---|---|
| standard Go, any OS | `net/http` + `crypto/tls` | yes |
| TinyGo on macOS | Network.framework | no |
| TinyGo on Linux | vendored mbedTLS | yes |
| other TinyGo targets | `ErrPlatformNotSupported` | — |
| `-tags force_tinygo_logic` | forces the native backend on host Go | |

`force_tinygo_logic` makes the native C code testable without a TinyGo
toolchain:

```bash
go test -tags force_tinygo_logic ./https
```

## Platform notes

### macOS

Uses Network.framework (`nw_connection_t`), which ships in the OS. There is
**no Homebrew or OpenSSL dependency**. Trust evaluation uses the system
keychain; custom anchors are applied through a `sec_protocol_options` verify
block.

The C bridge hand-declares the `nw_*`, `sec_*`, `CF*`, and `dispatch_*` symbols
it uses instead of including framework headers, because TinyGo compiles cgo C
files with `-nostdlibinc` and rejects `-F`/`-iframework` in `CFLAGS`. This also
decouples the build from the installed SDK version. `netdev/tls_openssl.h`
takes the same approach for OpenSSL.

Building with TinyGo needs either Xcode or the Command Line Tools installed at
their standard location; both paths are listed in the link flags and the linker
ignores whichever is absent. TinyGo honors neither `CGO_CFLAGS` nor
`CGO_LDFLAGS`, so these paths cannot be overridden from the environment.

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
