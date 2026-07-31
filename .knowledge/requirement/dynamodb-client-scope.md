---
id: requirement:dynamodb-client-scope
type: requirement
title: DynamoDB Client Scope
---
`nosql/dynamodb` covers single-table item and query operations against the JSON protocol, with the same credential and endpoint model as requirement:s3-client-scope.

```yaml
priority: must
state: shipped 2026-07-31, nosql/dynamodb
in_scope:
  items:
    - GetItem, PutItem, UpdateItem, DeleteItem
    - condition expressions, so ConditionalCheckFailedException is a first-class error
    - ReturnValues on write operations
  reads:
    - Query and Scan, one page per call
    - projection, filter and key-condition expressions with named placeholders
    - ConsistentRead
  batches:
    - BatchGetItem, BatchWriteItem
    - unprocessed keys and items returned to the caller, never retried inside the call
    - value: fewer round trips and fewer write units, not handshake avoidance; see decision:dynamodb-connection-policy
  tables:
    - CreateTable, DeleteTable, DescribeTable, ListTables
    - reason: a test suite and a first-run setup need them, mirroring CreateBucket
  credentials: same as requirement:s3-client-scope, static or environment
  endpoints:
    - AWS regional, https://dynamodb.<region>.amazonaws.com
    - DynamoDB Local and compatible servers over http, see decision:dynamodb-local-endpoint
out_of_scope:
  transactions:
    apis: TransactGetItems, TransactWriteItems
    reason: idempotency tokens and a second error taxonomy; the item APIs cover the common case
  partiql: ExecuteStatement and friends are a second request shape for the same operations
  streams_and_dax:
    reason: DynamoDB Streams is another service, and DAX needs endpoint discovery
  auto_pagination: >
    no paginator type. LastEvaluatedKey feeds ExclusiveStartKey explicitly, the
    same shape as the S3 continuation token in api:s3-client.
  waiters: DescribeTable is exposed; polling it is the caller's loop
  table_admin_extras: TTL, global tables, backup, autoscaling, tags
  index_management: >
    CreateTable accepts secondary indexes; UpdateTable index changes do not
retry: requirement:dynamodb-retry-policy
acceptance:
  - "met: unit tests pass under go test and go test -tags force_tinygo_logic"
  - >
    met: examples/dynamodbdemo, built with tinygo 0.41.1, runs the table
    lifecycle, a conditional write, a batch, paged queries and a delete against
    DynamoDB Local
  - a multi-byte string, a binary attribute, a nested map and an empty string round-trip
  - a Query spanning two pages returns the same items as one large page
  - a rejected condition expression surfaces ErrConditionalCheckFailed, not a generic 400
  - sequential calls on one client reuse one connection, asserted by counting accepts against a local server
decided_by: decision:no-aws-sdk-go-v2
evidence: requirement:dynamodb-driver-validation
