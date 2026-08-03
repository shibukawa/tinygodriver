---
id: decision:google-token-strategy
type: decision
title: Bearer Tokens Come From A Self-Signed JWT By Default
---
`cloud/google` mints its own bearer token by signing a JWT with the service-account key and sending it directly, instead of exchanging an assertion for an OAuth2 access token.

```yaml
state: proposed
proposed_on: 2026-08-02
measured_with: requirement:google-auth-validation
problem: >
  api:aws-signer computes a signature locally per request and never leaves the
  process. Google wants a bearer token, and the ordinary way to get one is a
  POST to a second host, which on this stack means a second TLS handshake to a
  second pool entry before the first Datastore call can go out.
chosen:
  default: self-signed JWT, RS256, sent as the Authorization bearer value
  claims:
    iss: the service account email
    sub: the same email
    aud: "https://datastore.googleapis.com/, the service host with a trailing slash"
    iat, exp: exp no more than an hour out
  why: >
    Google documents self-signed JWTs as an accepted bearer credential for Cloud
    APIs, with the service host as the audience. It removes the token endpoint
    entirely: no second host, no second handshake, no refresh failure mode that
    is separate from the request failure mode.
  constraint: aud and scope are mutually exclusive; this path sets aud only
  cache: >
    one token per process, reused until a minute before exp, re-signed on
    demand. At 3.6-4.2ms per signature under tinygo the refresh is not worth a
    background goroutine.
alternates:
  oauth2_exchange:
    what: POST the same assertion to https://oauth2.googleapis.com/token, grant type jwt-bearer
    kept_as: an option, because some deployments require a real access token
    cost: a second host in the connection pool and a round trip before the first call
  metadata_server:
    what: >
      GET http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token
      with a Metadata-Flavor header
    value: no key, no signature, no crypto/rsa in the binary
    fits: Cloud Run, GCE, GKE
    not_measured: requirement:google-auth-validation could not reach a GCE host
  static_token:
    what: WithToken or a caller-supplied TokenSource
    value: >
      the escape hatch for a device provisioned by a companion service, and the
      only path that links no signing code at all
    revised: >
      this entry read "the only path that links none of the ~590 KB of RSA
      code". decision:native-rsa-signing cut that to ~131 KB on darwin and to
      nothing on linux, where mbedTLS is already linked for TLS. Not signing is
      now a deployment argument, not a size argument.
  adc_user_credentials:
    what: the refresh_token in the file gcloud auth application-default login writes
    value: development convenience, and it needs no signing either
    limit: a user credential, not a service identity; not for production
  emulator: no credential at all, see decision:datastore-emulator-endpoint
rejected:
  golang_org_x_oauth2:
    reason: >
      google.CredentialsFromJSON pulls the token-source machinery and, through
      it, the same transport assumptions that defeat decision:no-cloud-google-go-datastore.
      The signing path this replaces is about eighty lines.
  signature_per_request:
    reason: >
      the shape SigV4 trained this repository to expect, but RSA is three orders
      of magnitude more expensive than HMAC and Google does not accept it
  refresh_goroutine:
    reason: >
      a background refresher outlives the client unless Close stops it, and
      api:datastore-client already carries a lifecycle obligation it does not
      want a second one of
consequences:
  - >
    clock skew becomes a failure mode. A device with a wrong clock mints a token
    the server rejects as expired, and the error is UNAUTHENTICATED, which
    requirement:datastore-retry-policy does not retry. This has no equivalent in
    the SigV4 path, where skew is also fatal but the failure names itself.
  - >
    the token is a bearer credential in memory for an hour, unlike a SigV4
    signature that is worthless the moment its request is sent
  - >
    audience is per-service, so a second Google service client cannot reuse the
    same cached token
verifies: requirement:google-auth-validation
surface: api:google-auth
