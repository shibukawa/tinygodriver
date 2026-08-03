---
id: system:google-datastore
type: system
title: Firestore In Datastore Mode
---
The Datastore v1 API, the wire surface Firestore exposes when a database is created in Datastore mode. Verified against the published REST reference on 2026-08-02.

```yaml
endpoint: https://datastore.googleapis.com
shape: >
  POST only, one RPC per request, JSON body, operation in the URL path rather
  than a header. Closer to system-level RPC than to REST resources.
methods:
  - "POST /v1/projects/{projectId}:lookup"
  - "POST /v1/projects/{projectId}:runQuery"
  - "POST /v1/projects/{projectId}:runAggregationQuery"
  - "POST /v1/projects/{projectId}:beginTransaction"
  - "POST /v1/projects/{projectId}:commit"
  - "POST /v1/projects/{projectId}:rollback"
  - "POST /v1/projects/{projectId}:allocateIds"
  - "POST /v1/projects/{projectId}:reserveIds"
  - "operations.* for admin long-running jobs, out of scope"
transports:
  grpc: what the official client uses, see decision:no-cloud-google-go-datastore
  rest_json: >
    a first-class published transport, proto3 JSON mapping. This is what a
    hand-written client targets.
auth:
  scheme: "Authorization: Bearer <token>"
  detail: decision:google-token-strategy
  contrast: >
    unlike api:aws-signer there is no per-request signature. One token covers an
    hour of requests, and getting it is the expensive part.
database_selection:
  databaseId: request-level field; empty means the default database
  partitionId: "{projectId, namespaceId}, carried on every key"
data_model:
  kind: an entity's type name, created implicitly on first write
  key: a path of kind plus id or name pairs, so ancestry is part of identity
  schema: none; properties differ freely between entities of one kind
  indexes: >
    single-property indexes are automatic; composite indexes are declared out of
    band and are an admin-API concern, not a data-API one
errors:
  body: '{"error": {"code": int, "message": string, "status": string}}'
  discrimination: the status string, a canonical gRPC code name
  codes:
    retryable: ABORTED 409, DEADLINE_EXCEEDED 504, UNAVAILABLE 503, RESOURCE_EXHAUSTED 429
    once_only: INTERNAL 500
    terminal: INVALID_ARGUMENT 400, FAILED_PRECONDITION 400, NOT_FOUND 404, ALREADY_EXISTS 409, PERMISSION_DENIED 403, UNAUTHENTICATED 401
  note: >
    ABORTED and ALREADY_EXISTS share HTTP 409 and differ only in status, so
    status-string matching is required, not optional
  no_checksum: >
    no x-amz-crc32 equivalent. requirement:datastore-retry-policy therefore has
    no integrity layer to carry.
limits:
  entity: 1 MiB minus 4 bytes
  entity_key: 6 KiB
  lookup_keys: 1000 per call
  request: 10 MiB
  transaction: 10 MiB
  nesting: 20 levels of entity value
  indexed_string: 1500 bytes before it stops being indexed
consistency:
  default: strong, including non-ancestor queries, which is the Firestore backend behaviour
  option: readOptions.readConsistency EVENTUAL, or a transaction handle, or readTime
emulator: decision:datastore-emulator-endpoint
consumed_by: api:datastore-client
compared_with: concept:dynamodb-datastore-mapping
