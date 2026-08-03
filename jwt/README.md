# jwt

`jwt` strictly parses, signs, and verifies a bounded signed JWT subset for Go
and TinyGo. Verification requires an explicit algorithm allowlist and a resolver
that returns a key bound to the same algorithm. `alg=none`, unsupported critical
headers, duplicate JSON members, non-canonical Base64url, ambiguous JWKS
matches, and oversized input are rejected.

Supported algorithms are HS256 and RS256, in both directions. ES256, EdDSA, and
JWE are not supported.

## It is a JWS implementation

A JWT is claims serialized as a JWS, so the three-segment `header.payload.signature`
form is JWS Compact Serialization (RFC 7515) by definition. That is what this
package implements — `Token.signingInput` is the JWS Signing Input of section 2,
under that name — and there is no separate JWS layer to add.

The scope is the JWT subset of JWS only. `Sign` takes claims, not bytes, so JSON
Serialization, detached payloads, multiple signatures, and non-JSON payloads are
all absent. If a caller ever needs to sign something that is not a claim set,
the honest change is to extract an internal core holding the signing input, the
compact serialization, and the algorithm dispatch, leaving this package as the
claims layer on top. Renaming it to `jws` would be the wrong fix: the package is
offset from JWS in both directions, doing more than JWS (it validates `iss`,
`aud`, `exp`, `nbf`, `iat`) and less (no arbitrary payloads).

## Provenance and divergence

This package came from `github.com/shibukawa/popcornwave/contrib/jwt`, which
implements the verifier half for resource servers. One change was made here:

- **RS256 signing.** Upstream `Sign` accepted `HS256` only and rejected every
  other algorithm outright, while RS256 verification already existed. This copy
  widens the guard to the same set `verifySignature` accepts.

Nothing else was altered. `jwks.go` and `verify.go` are carried unchanged even
though a client that only mints tokens never calls them; the linker drops what
is unused, so keeping the package whole costs maintenance clarity rather than
binary size.

`internal/authn` carries the upstream bounded Base64url and JSON validation
helpers. `http.go` was not copied: this package never referenced it, and it
would pull `net`, `net/http`, and `net/url` into a TinyGo build for nothing.

## Signing with RS256

This package holds no RSA code. A signer supplies it, which is what keeps `jwt`
free of build tags and cgo:

```go
signer, err := google.NewRSASigner(credentials) // implements jwt.Signer
token, err := jwt.Sign(jwt.Header{KeyID: keyID}, claims, signer)
```

`cloud/google` provides such a signer over `internal/rsasign`, which uses
`crypto/rsa` on host Go and the OS crypto library on TinyGo builds.
