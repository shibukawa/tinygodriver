---
id: flow:datastore-request
type: flow
title: Datastore Request Round Trip
---
One `Get` from call to decoded entity on the TinyGo path. The difference from flow:dynamodb-request is the token branch at the front.

```yaml
flow:
  - id: call
    actor: application
    action: client.Get(ctx, key)
    next: token
  - id: token
    actor: api:google-auth
    action: read the cached token, or mint one
    detail: >
      a cache hit is free. A miss signs a JWT in 3.6-4.2ms under tinygo, per
      requirement:google-auth-validation, or costs a round trip to a second host
      on the exchange and metadata paths
    branch: decision:google-token-strategy
    next: build
  - id: build
    actor: api:datastore-client
    action: marshal LookupRequest, one member per value
    detail: data:datastore-value
    next: request
  - id: request
    actor: api:datastore-client
    action: 'POST "/v1/projects/{p}:lookup" with the bearer header, body buffered'
    next: send
  - id: send
    actor: api:https-transport
    action: lease a pooled connection or dial, write, read response
    detail: flow:connection-lease
    on_error: >
      the transport replays once on a dead pooled connection, then this client
      retries per requirement:datastore-retry-policy
    next: classify
  - id: classify
    actor: api:datastore-client
    action: 200 continues, otherwise decode error.status and map to a sentinel
    on_error: >
      retry if the status is retryable, refresh the token once on
      UNAUTHENTICATED, else return *datastore.Error
    next: decode
  - id: decode
    actor: api:datastore-client
    action: read found, missing and deferred; a one-key miss is ErrNoSuchEntity
    next: done
  - id: done
    actor: application
    action: read properties through AsString, AsInt, or UnmarshalEntity
notes:
  - no signing step; the request is not covered by a signature, unlike flow:dynamodb-request
  - no checksum verification step; system:google-datastore sends none
  - the connection returns to the pool at step done, per requirement:connection-reuse
  - std go replaces step send with net/http, per rule:build-tag-selection
  - a transaction inserts beginTransaction before build and commit after done, unless it is single-use
