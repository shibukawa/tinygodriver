# httpsdemo

Runs a handful of checks against a real HTTPS server and reports what the
current platform does.

**The source is identical on every platform.** No build tags, no conditional
imports, no `runtime.GOOS` branches. The TLS backend underneath differs —
Network.framework on macOS, vendored mbedTLS on Linux, `crypto/tls` in standard
Go builds — and only the client-certificate check can tell them apart.

```bash
go run ./examples/httpsdemo

tinygo build -o httpsdemo ./examples/httpsdemo
./httpsdemo

URL=https://your-host/ ./httpsdemo
```

Needs outbound HTTPS.

## What it checks

| Check | What it proves |
|---|---|
| system trust | An ordinary `https.Get` verifies against the platform trust store |
| verification enforced | `WithRootCAsOnly(true)` with no anchors trusts nothing, so a success here would mean verification is being skipped |
| skip verify | The escape hatch works and is opt-in |
| client certificates | Whether this backend supports mTLS, and that an unsupported backend **refuses** rather than silently ignoring the certificate |
| deadline | A request context deadline reaches the TLS layer instead of hanging |

## Expected output

macOS, TinyGo:

```text
platform : darwin/arm64, compiler tinygo
target   : https://example.com/

ok   system trust           200 OK, 559 bytes
ok   verification enforced  rejected: certificate invalid
ok   skip verify            200 OK
ok   client certificates    not supported on this backend, and refused rather than ignored
ok   deadline               gave up after 1ms

all 5 checks passed
```

Linux and standard Go builds report `client certificates supported` instead.
That single line is the only platform-visible difference.

## Two notes on portability

**Use a request context for deadlines, not `http.Client.Timeout`.** TinyGo's
`net/http` drops the `setRequestCancel` machinery that carries `Client.Timeout`
to a custom `RoundTripper`, so `Client.Timeout` is silently ignored there. A
context deadline, or `Transport.DialTimeout` / `Transport.ResponseTimeout`,
works on every compiler. This demo uses a context for exactly that reason.

**No `netdev` import is needed.** The `https` package reaches the network on its
own on both backends. Blank-import `netdev` only if the program also uses
`net` or `net/http` directly.
