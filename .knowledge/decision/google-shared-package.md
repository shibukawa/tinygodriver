---
id: decision:google-shared-package
type: decision
title: Credentials And Tokens Live In cloud/google
---
Google credentials, token minting and HTTP backend selection go in a public `cloud/google` package, mirroring what decision:aws-shared-package did for AWS, with the service client in `nosql/datastore`.

```yaml
state: proposed
proposed_on: 2026-08-02
layout:
  cloud/google: api:google-auth
  nosql/datastore: api:datastore-client
naming:
  cloud/google: >
    matches cloud/aws. The vendor, not the product, because the credential model
    is the vendor's and a second Google service would share it.
  nosql/datastore: >
    the API is literally Datastore v1, so the package is named for the wire, not
    for the product page. Firestore in Datastore mode is what it talks to;
    Firestore native mode is a different API and would be a different package.
  rejected:
    nosql/firestore: >
      would have to be renamed or overloaded the day native mode is added, and
      the two APIs share no request shape
    cloud/google/datastore: buries a client under a credentials package, refused once already for AWS
files:
  cloud/google:
    - credentials.go: the service_account file shape, CredentialsFromEnv, CredentialsFromJSON
    - jwt.go: >
        RSA key handling and the RS256 Signer. The claim and compact-serialization
        half comes from the moved-in package; see decision:jwt-package-reuse.
    - token.go: TokenSource, the cache, the oauth2 exchange and metadata paths
    - transport.go, transport_std.go, transport_native.go: copied in shape from cloud/aws
  nosql/datastore:
    - client.go, api.go: the eight RPCs
    - value.go: data:datastore-value
    - key.go: keys, paths, namespaces
    - query.go: filter and order builders
    - errors.go: the canonical-code mapping
    - entity.go: the optional reflect-based struct mapper
build_split:
  reason: >
    a build that supplies its own token should not link a signer. The stakes
    dropped once decision:native-rsa-signing landed, from ~590 KB to ~131 KB on
    darwin and to nothing on linux, but the lever is free so it stays.
  method: >
    keep signing behind a constructor a caller must name. A program using only
    WithTokenSource never references jwt.go, so the linker drops it, which is
    the same lever decision:dynamodb-json-codec pulls for reflection.
  not_chosen: a build tag, which would make the choice a build-time question rather than a call-site one
signer_lives_elsewhere: >
  the RSA operation is in internal/rsasign, not here; see api:rsa-signer. It is
  per-OS code and cloud/google is not, so putting it here would drag the cgo
  build constraints of rule:tinygo-darwin-toolchain into a package that
  otherwise has none.
what_is_not_shared_with_cloud_aws:
  - >
    nothing. The two packages have the same job and no common code: a signature
    and a bearer token have no shared abstraction worth writing.
  - >
    an interface over both was considered and refused. It would exist to serve a
    caller who wants to swap clouds, which is not a caller this repository has.
reuses_from_cloud_aws:
  - the ClientOptions and NewHTTPClient shape, so pool tuning is one field on both
  - requirement:connection-reuse, unchanged
  - rule:build-tag-selection for the std and native transport split
precedent: decision:aws-shared-package, decision:package-layout
