---
id: decision:datastore-write-preconditions
type: decision
title: Conditional Writes Are Preconditions And Transactions, Not An Expression Language
---
The nearest thing to a DynamoDB condition expression is a mutation precondition, and where that is not enough the client exposes a read-write transaction rather than inventing a condition dialect.

```yaml
state: proposed
proposed_on: 2026-08-02
what_the_wire_offers:
  insert: fails with ALREADY_EXISTS if the key exists; this is put-if-absent
  update: fails with NOT_FOUND if the key does not exist; this is put-if-present
  upsert: no precondition
  baseVersion: the mutation applies only if the stored version matches
  updateTime: the same idea keyed on a timestamp instead of a version
  conflictResolutionStrategy: SERVER_VALUE or FAIL, per mutation
  what_is_missing: >
    any predicate over property values. There is no attribute_not_exists(x),
    no size(x) < n, no comparison against a stored field.
chosen:
  level_1: >
    expose the three mutation verbs directly. Insert and Update carry their
    preconditions in the verb, which covers the two conditions callers actually
    write most often.
  level_2: >
    expose baseVersion and updateTime as write options, sourced from the version
    and updateTime a Lookup already returns. This is optimistic concurrency, and
    it is the honest name for it.
  level_3: >
    a read-write transaction for anything predicate-shaped. Read inside the
    transaction, decide in Go, commit. The predicate runs in the client, which
    is why the transaction is required and not optional.
  api: "func (c *Client) RunInTransaction(ctx, func(tx *Tx) error, opts ...TxOption) error"
why_not_emulate_condition_expressions:
  - >
    a client-side predicate over a value the client read outside a transaction
    is a race with a confident-looking API, which is worse than no API
  - >
    the DynamoDB spelling would imply server-side evaluation, and nothing on
    this wire evaluates it
  - >
    baseVersion is strictly stronger for the read-modify-write case, because it
    catches a concurrent write the caller never read
transaction_cost:
  round_trips: beginTransaction, then the reads, then commit; three at minimum
  single_use: >
    readOptions.newTransaction and CommitRequest.singleUseTransaction fold the
    begin into the first call, which the client uses whenever the transaction is
    one read plus one commit
  retry: >
    ABORTED means contention and the whole closure re-runs, not just the commit.
    See requirement:datastore-retry-policy, which is where that loop lives.
  bound: >
    the closure must be side-effect free outside the transaction, since it can
    run several times. Stated in godoc, not enforceable.
consequences:
  - >
    requirement:datastore-client-scope includes transactions, reversing the call
    requirement:dynamodb-client-scope made for DynamoDB. The difference is that
    there transactions were a convenience; here they are the only conditional path.
  - >
    a caller porting from nosql/dynamodb rewrites conditional writes; see
    concept:dynamodb-datastore-mapping
  - >
    ALREADY_EXISTS and ABORTED are both HTTP 409 and mean opposite things, one
    terminal and one retryable, so the error mapping cannot key on status code
related: system:google-datastore, api:datastore-client
