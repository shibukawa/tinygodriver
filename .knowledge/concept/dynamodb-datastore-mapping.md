---
id: concept:dynamodb-datastore-mapping
type: concept
title: What Transfers From DynamoDB To Datastore, And What Does Not
---
"Equivalent to the DynamoDB driver" is a statement about effort and shape, not about API parity. This records which parts of api:dynamodb-client have a Datastore counterpart and which have none, so the gaps are decided once rather than rediscovered per operation.

```yaml
same_shape:
  transport: POST plus JSON over api:https-transport, one operation per request
  codec: a one-member union type, hand-encoded; data:datastore-value
  int64_as_text: DynamoDB N and proto3 integerValue, for the same precision reason
  pagination: an opaque cursor the caller feeds back, no paginator type
  pooling: one host, many small requests; decision:dynamodb-connection-policy applies unchanged
  errors: a typed error with a discriminating string and sentinel values
  lifecycle: Close releases pooled native TLS handles
different:
  auth:
    dynamodb: a local SigV4 signature per request
    datastore: a bearer token, minted once an hour; decision:google-token-strategy
  identity:
    dynamodb: table plus partition key and optional sort key
    datastore: >
      kind plus a key path, where ancestors are part of the key. There is no
      sort key; ordering is a query concern, not an identity one.
  namespaces:
    dynamodb: none
    datastore: a namespaceId on every key, a tenancy dimension with no analogue
  batching:
    dynamodb: GetItem is single, BatchGetItem is the batch form
    datastore: >
      lookup is batch by construction and takes a key list, so the single-key
      call is the special case rather than the other way round
  writes:
    dynamodb: PutItem, UpdateItem and DeleteItem are separate operations
    datastore: >
      one commit carries a mutation list of insert, update, upsert and delete.
      The four verbs are members of a request, not endpoints.
  partial_update:
    dynamodb: an UpdateItem expression touching named attributes
    datastore: >
      update replaces the whole entity. propertyMask narrows it, but there is no
      server-side arithmetic, so the ADD idempotency hazard in
      requirement:dynamodb-retry-policy has no Datastore equivalent.
  conditions:
    dynamodb: a condition expression on any write
    datastore: >
      insert-fails-if-exists, plus a baseVersion or updateTime precondition.
      Anything richer needs a transaction; see decision:datastore-write-preconditions.
  transactions:
    dynamodb: excluded from requirement:dynamodb-client-scope as an extra taxonomy
    datastore: >
      included, because they are the only way to express a read-modify-write.
      The same reasoning that excluded them there includes them here.
  integrity:
    dynamodb: x-amz-crc32 on every reply
    datastore: nothing; requirement:datastore-retry-policy loses a layer
  tables:
    dynamodb: CreateTable, DeleteTable, DescribeTable, ListTables
    datastore: >
      none. Kinds are implicit and composite indexes are an admin-API concern.
      The first-run setup those calls existed for does not exist here, so
      requirement:datastore-client-scope drops them rather than emulating them.
  throughput:
    dynamodb: provisioned capacity, so throttling is normal operation
    datastore: >
      contention instead. ABORTED on a hot key is the analogue of
      ProvisionedThroughputExceededException, and it is a transaction-level
      retry rather than a request-level one.
no_analogue_either_way:
  datastore_only: [ancestor queries, projection queries with distinctOn, GQL, allocateIds, geo points]
  dynamodb_only: [secondary index management, TTL, streams, conditional expression language]
consequence: >
  a caller porting from nosql/dynamodb rewrites the write path and keeps the
  read path. Naming the operations after DynamoDB to soften that was rejected in
  api:datastore-client, because the semantics do not follow the names.
