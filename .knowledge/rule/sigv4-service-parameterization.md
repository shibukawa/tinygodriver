---
id: rule:sigv4-service-parameterization
type: rule
title: One Signer, Service Passed In
---
The service name is an argument to signing, not a constant, and the two places it appears must always agree.

```yaml
service_name_appears_in:
  credential_scope: <date>/<region>/<service>/aws4_request
  signing_key: HMAC chain AWS4<secret> -> date -> region -> service -> aws4_request
  failure_mode: >
    a mismatch between the two is a SignatureDoesNotMatch that no local test
    catches, because both sides of a self-test would use the same wrong value
path_canonicalization:
  s3: >
    single-encode. The canonical URI is the path as sent, so URIEncode output
    goes on the wire; see rule:sigv4-wire-agreement.
  other_services: >
    the SigV4 spec normalizes and double-encodes the path. DynamoDB posts to
    "/", where both rules produce "/", so the distinction is currently
    unobservable. Any new service with a real path must set this explicitly
    rather than inherit the S3 behaviour.
  rule: the signer takes the choice as a field, never infers it from the host
signed_headers:
  always: host, x-amz-date, and every x-amz-* header on the request
  s3_extra: content-encoding, content-md5, content-type, range
  dynamodb: content-type plus x-amz-target, which the x-amz-* sweep already covers
  never: >
    Content-Length. net/http writes it from Request.ContentLength, not from the
    header map, so signing it produces a header the server does not receive.
payload_hash:
  dynamodb: always the real sha256; the body is a small buffered JSON document
  s3: body hash, or UnsignedPayload when the caller opts in
  rule: UnsignedPayload is an S3 affordance; other services reject it
session_token: x-amz-security-token is set before the canonical headers are collected, so it is signed
tested_by:
  - >
    TestSignDynamoDBExample and TestSignDynamoDBWithSessionToken, whose expected
    values came from aws-sdk-go-v2 signer/v4 over the same request and the same
    header set. Matching a self-computed value would prove nothing; matching the
    SDK is what makes them evidence.
  - >
    TestSignServiceReachesScopeAndKey, which asserts that changing only the
    service changes the signature, not just the printed scope. It is the one
    test that would catch a service name reaching the scope but not the key.
  - TestSignDoubleEncodePath, same shape, for the canonicalization flag
  - TestSignHonoursRequestHost, for the host the signature covers
  - the s3 tests covering rule:sigv4-wire-agreement pass unchanged
sdk_header_difference:
  observed: >
    aws-sdk-go-v2 signs content-length and omits x-amz-content-sha256 for
    dynamodb; this signer does the opposite. Both are valid: the server verifies
    whatever SignedHeaders names, provided those headers are the ones sent.
  consequence: >
    a known-answer test against the SDK has to align the header set first, or it
    compares two correct signatures over different canonical requests
applies_to: cloud/aws
