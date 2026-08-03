---
id: decision:datastore-emulator-endpoint
type: decision
title: Integration Tests Run Against The Datastore Emulator
---
The integration suite targets the gcloud Datastore emulator, the way the DynamoDB suite targets DynamoDB Local and the S3 suite targets RustFS.

```yaml
state: proposed
proposed_on: 2026-08-02
chosen: gcloud beta emulators datastore start, in the google/cloud-sdk container
protocol: http, no TLS, no credential
env:
  DATASTORE_EMULATOR_HOST: host and port, read by api:datastore-client as the endpoint override
  DATASTORE_PROJECT_ID: the project the emulator answers for
port: >
  dynamically assigned by default and re-assigned on restart, so the suite pins
  one with --host-port rather than parsing env-init output
auth: >
  none. The emulator ignores the Authorization header, which means the token
  path of api:google-auth is not exercised locally at all. This is a sharper gap
  than decision:dynamodb-local-endpoint has, where the signature is at least
  present and well formed.
consequences:
  - the JSON codec, query, transaction and error paths are all covered offline
  - >
    decision:google-token-strategy is not covered offline. It needs one manual
    run against the real endpoint, which is where a wrong audience or a clock
    problem would show up.
  - contention cannot be provoked reliably, so ABORTED retry tests use a stub server
  - >
    the emulator is a reimplementation, not the shipped engine, so query planner
    and index behaviour may differ from production
rejected:
  firestore_emulator:
    reason: >
      it serves Firestore native mode, a different API. Datastore mode needs the
      Datastore emulator specifically.
  real_gcp_only: needs a project and a key in CI, and it costs money per run
  no_integration_tests: >
    rule:sigv4-wire-agreement and decision:dynamodb-local-endpoint both recorded
    the same finding: only a real server catches a misread specification. The
    DescribeTable reply shape is the standing example.
dependency_cost: >
  the emulator is a Java process inside the gcloud SDK image, heavier than
  amazon/dynamodb-local. Accepted, because the alternative is no offline coverage.
gcp_verification: >
  one manual run against datastore.googleapis.com per release, covering the
  token path, the real error bodies, and the transport cost this repository
  measures for every driver; see requirement:google-auth-validation for what is
  still unmeasured
related: requirement:test-strategy, decision:dynamodb-local-endpoint
