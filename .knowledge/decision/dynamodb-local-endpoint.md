---
id: decision:dynamodb-local-endpoint
type: decision
title: Integration Tests Run Against DynamoDB Local
---
The integration suite targets `amazon/dynamodb-local`, the same way the S3 suite targets RustFS.

```yaml
state: proposed
proposed_on: 2026-07-30
chosen: amazon/dynamodb-local in docker, port 8000, http
invocation: -jar DynamoDBLocal.jar -inMemory -sharedDb
flags:
  sharedDb: >
    required. Without it the server partitions data by access key and region, so
    a test that creates a table with one credential cannot see it with another.
  inMemory: no volume to clean between runs
credentials: any non-empty pair; the signature is not verified, but it must be present and well formed
consequences:
  - the signer is exercised over http, which proves signing does not depend on TLS
  - >
    x-amz-crc32 is present, contrary to the assumption this decision was drafted
    with. Verified 2026-07-31; the checksum path is therefore exercised locally
    rather than only against AWS.
  - server-side throttling cannot be provoked, so retry tests use a stub server
value_demonstrated: >
  the first run found DescribeTable decoding nothing: the reply carries Table
  while CreateTable carries TableDescription, and the unit test had been written
  against the same wrong assumption as the code. A fixture cannot catch a
  misread specification; a real server can.
rejected:
  localstack: >
    covers many services, but it is a much larger dependency for one, and its
    DynamoDB layer is a reimplementation rather than the shipped engine
  real_aws_only: needs an account in CI and costs money per run
  no_integration_tests: >
    the S3 work showed that only a real endpoint catches encoding and signature
    mistakes; see rule:sigv4-wire-agreement
aws_verification: >
  one manual run against a real regional endpoint per release, since the local
  server accepts signatures it should reject; the transport measurement in
  requirement:dynamodb-driver-validation is that run's other half
related: requirement:test-strategy
