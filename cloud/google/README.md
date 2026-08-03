# google — credentials and bearer tokens for TinyGo

`cloud/google` is what every Google Cloud client in this repository needs:
credentials, bearer tokens, and the HTTP client the build selects. It is the
counterpart of [`cloud/aws`](../aws), and it is shaped by the one real
difference between the two clouds.

AWS signs each request locally, and the signature never leaves the process.
Google wants a bearer token, and the ordinary way to get one is a POST to
`oauth2.googleapis.com` — which on this stack means a second TLS handshake to a
second pool entry before the first real call can go out.

So the default here is a **self-signed JWT**: sign a claim set with the service
account key, send it as the bearer value, and skip the token endpoint entirely.

```go
creds, err := google.CredentialsFromEnv()          // GOOGLE_APPLICATION_CREDENTIALS
src, err := google.JWTTokenSource(creds, "https://datastore.googleapis.com/")
defer src.Close()

tokens := google.Cached(src)
token, err := tokens.Token(ctx)
req.Header.Set("Authorization", "Bearer "+token.Value)
```

## Token sources

| Source | Round trips | Links RSA code | Where it fits |
| --- | --- | --- | --- |
| `JWTTokenSource` | none | yes | the default; one audience per source |
| `OAuth2TokenSource` | one, to the token endpoint | yes | deployments that require a real access token |
| `MetadataTokenSource` | one, to the metadata server | no | GCE, Cloud Run, GKE |
| `StaticTokenSource` | none | no | a device provisioned by a companion service, and tests |

The audience for a self-signed JWT is the service host **with a trailing
slash**, and it is per-service: a token minted for Datastore is not accepted by
another API. `aud` and `scope` are mutually exclusive on that path, so
`JWTTokenSource` sets only `aud` and `OAuth2TokenSource` sets only `scope`.

`Cached` refreshes 60 s before expiry, on the calling goroutine, under a mutex:
one refresh is in flight and the others wait for it. There is no background
refresher — it would outlive the client unless something stopped it, and TinyGo
goroutines are not OS threads, so one cannot be relied on to make progress while
another blocks in a socket call.

## Signing

The RSA operation is not in this package. It is in `internal/rsasign`, which
uses `crypto/rsa` on host Go and the OS crypto library on TinyGo builds. That
split is about binary size: in a real client, forcing the pure-Go path costs
**357 KB**, which is more than the entire darwin HTTPS client.

Only `JWTTokenSource` and `OAuth2TokenSource` reference the signing code, so a
binary built with `StaticTokenSource` or `MetadataTokenSource` drops it.

`SignerBackend()` reports which implementation this build selected;
`Backend` reports the HTTP stack, matching `cloud/aws`.

RS256 only. ES256 is not implemented.

## Clocks

One failure mode has no AWS equivalent and is worth stating plainly.

A self-signed JWT is only valid against the server's clock. A device whose clock
is wrong mints a token the server rejects as expired, reported as
`UNAUTHENTICATED` — which is **not** a retryable status. The likeliest cause of
that status in the field is the clock, not the key.

A service client can call `CachedSource.Invalidate` and resend once on a 401,
which is what `nosql/datastore` does. That recovers from a token that expired
early; it does not recover from a clock that is hours out.

## Credentials

`CredentialsFromEnv` reads the file named by `GOOGLE_APPLICATION_CREDENTIALS`.
That is the whole of the resolution.

The full Application Default Credentials search also consults a well-known
gcloud config path, the GCE metadata server, and external account files. Each is
a different credential kind with a different failure mode, and a client this size
is better off being told which one it has. `MetadataTokenSource` covers the GCE
case explicitly.

`ProjectIDFromEnv` reads `GOOGLE_CLOUD_PROJECT`, then `DATASTORE_PROJECT_ID`.
`EmulatorHost("datastore")` reads `DATASTORE_EMULATOR_HOST`, the variable the
gcloud emulators set. That value carries no scheme; emulators speak plain HTTP.

## What is not here

- No credential chain discovery beyond the environment variable and an explicit file
- No impersonation, workload identity federation, or external account files
- No per-service endpoint resolution; a service package builds its own URL
- No retry policy, which is per-service
- No request model or serialization, which stays in the service package

## Testing

```sh
go test ./cloud/google/
go test -tags force_tinygo_logic ./cloud/google/
```

The tests carry no build tag, so the signing path runs against `crypto/rsa` on
the first command and against the OS backend on the second. A token that only
mints correctly on one of them fails.

The self-signed JWT is checked claim by claim and then verified through the
`jwt` verifier, so a signature that is well formed but wrong cannot pass on
shape alone. What that does **not** cover is whether Google accepts it: the
Datastore emulator ignores `Authorization` entirely, so the token path needs one
manual run against a real endpoint.
