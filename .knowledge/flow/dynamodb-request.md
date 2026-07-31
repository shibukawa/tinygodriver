---
id: flow:dynamodb-request
type: flow
title: DynamoDB Request Round Trip
---
One `GetItem` from call to decoded item on the TinyGo path.

```yaml
flow:
  - id: call
    actor: application
    action: client.GetItem(ctx, "users", key)
    next: build
  - id: build
    actor: api:dynamodb-client
    action: marshal the operation input to JSON, one member per attribute
    detail: data:dynamodb-attribute-value
    next: request
  - id: request
    actor: api:dynamodb-client
    action: POST "/" with Content-Type and X-Amz-Target, body buffered
    next: sign
  - id: sign
    actor: api:aws-signer
    action: Sign with Service "dynamodb" and the sha256 of the body
    detail: rule:sigv4-service-parameterization
    next: send
  - id: send
    actor: api:https-transport
    action: lease a pooled connection or dial, write, read response
    detail: flow:connection-lease
    on_error: >
      the transport replays once on a dead pooled connection, then this client
      retries per requirement:dynamodb-retry-policy
    next: verify
  - id: verify
    actor: api:dynamodb-client
    action: read the whole body, compare crc32 against x-amz-crc32
    on_error: retry, then ErrChecksumMismatch
    next: classify
  - id: classify
    actor: api:dynamodb-client
    action: 200 continues, otherwise decode __type and map to a sentinel
    on_error: retry if the type is retryable, else return *dynamodb.Error
    next: decode
  - id: decode
    actor: api:dynamodb-client
    action: unmarshal Item; absent Item is ErrItemNotFound
    next: done
  - id: done
    actor: application
    action: read attributes through AsString, AsInt, or UnmarshalItem
notes:
  - the connection returns to the pool at step done, per requirement:connection-reuse
  - reading the body to EOF is what makes it reusable, so an abandoned reply costs the next call a handshake
  - std go replaces step send with net/http, per rule:build-tag-selection
  - no redirect handling; DynamoDB has no region redirect, unlike requirement:s3-redirect-resigning
