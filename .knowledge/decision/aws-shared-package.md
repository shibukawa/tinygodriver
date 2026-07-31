---
id: decision:aws-shared-package
type: decision
title: SigV4 And Credentials Move To cloud/aws
---
Signing, credentials, environment resolution and HTTP backend selection move out of `storage/s3` into a public `cloud/aws` package, and each service client sits in its own package.

```yaml
state: accepted
accepted_on: 2026-07-31
implemented: cloud/aws, with storage/s3 rebuilt on it and its tests unchanged
trigger: nosql/dynamodb needs the same signer, and duplicating it would let the two copies drift
layout:
  cloud/aws: api:aws-signer
  nosql/dynamodb: api:dynamodb-client
  storage/s3: unchanged import path, now a consumer of cloud/aws
moves:
  - credentials.go: Credentials, CredentialsFromEnv
  - sign.go: sign, uriEncode, canonicalQuery, hmac and sha256 helpers
  - transport_std.go, transport_native.go: Backend, newHTTPClient, now also carrying the pool settings of requirement:connection-reuse
  - redirect_hostgo.go, redirect_tinygo.go: the no-redirect policy helper
stays_in_storage_s3:
  - the S3 REST request builders and XML decoding
  - requirement:s3-redirect-resigning, which is S3 region behaviour, not signing
  - the S3 error document mapping
compatibility:
  method: type alias and thin wrappers, so no caller of storage/s3 changes
  surface: |
    type Credentials = aws.Credentials
    func CredentialsFromEnv() Credentials { return aws.CredentialsFromEnv() }
    const Backend = aws.Backend
    var ErrNoCredentials = aws.ErrNoCredentials   // and ErrNoRegion
  requirement: the s3 public API and its test names stay as they are
  error_split:
    shared: ErrNoCredentials and ErrNoRegion, which are configuration failures
    per_service: >
      ErrBadCredentials stays in each service package, because it is a decoded
      wire response and its message names the service that got it
  test_split: >
    the signer tests moved to cloud/aws; storage/s3 keeps the request-building
    tests, renamed request_test.go, which is what rule:sigv4-wire-agreement is
    actually about
visibility:
  chosen: public cloud/aws
  reason: >
    a user needing another AWS service can sign requests without waiting for a
    client package here, and the surface is small enough to keep stable
  rejected:
    internal/aws: >
      shareable inside the module and invisible outside, but a signer is the
      generally useful half of this work
    duplicate_per_service: >
      two signers means two chances to break rule:sigv4-wire-agreement, and that
      bug only reproduces against real AWS
naming:
  cloud/aws: matches storage/s3 and database/sql/* category-then-implementation
  nosql/dynamodb: >
    database/sql/* is reserved for database/sql drivers, which DynamoDB is not.
    A sibling top-level category keeps that distinction visible.
  rejected:
    cloud/aws/dynamodb: buries a client under a credentials package
    database/nosql/dynamodb: implies a database/sql relationship that does not exist
precedent: decision:package-layout, rule:build-tag-selection
consequences:
  - rule:build-tag-selection precedent list points at cloud/aws, not storage/s3
  - rule:sigv4-service-parameterization becomes the shared signer contract
  - one signer test suite covers both services
