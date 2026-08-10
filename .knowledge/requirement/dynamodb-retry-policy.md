---
id: requirement:dynamodb-retry-policy
type: requirement
title: Retry, Throttling And Response Integrity
---
DynamoDB expects the client to retry throttled and corrupted requests; unlike `storage/s3`, the driver cannot leave this to the caller.

```yaml
priority: must
reason: >
  ProvisionedThroughputExceededException is a normal operating condition on a
  provisioned table, not a fault. A client without backoff turns a brief hot
  partition into an application error.
retryable:
  status:
    500: InternalServerError
    503: ServiceUnavailable
  types:
    - ProvisionedThroughputExceededException
    - ThrottlingException
    - RequestLimitExceeded
    - TransactionConflictException
    - crc32 mismatch, treated as a transport fault
not_retryable:
  - ValidationException, ResourceNotFoundException, ConditionalCheckFailedException
  - any 4xx not named above, including signature and credential failures
policy:
  default: 3 attempts, exponential backoff with jitter, 25ms base, 1s cap
  bound: >
    total elapsed never exceeds one operation timeout. One context is derived
    before the first attempt, so retries, backoff, checksum handling, and body
    reads all consume the same budget, including with a supplied HTTP client.
  option: WithRetry(attempts int, base time.Duration); zero attempts disables it
  idempotency: >
    only whole requests are retried, and only for the errors above. A retried
    PutItem without a condition expression is idempotent by construction; an
    UpdateItem with ADD is not, so a caller doing arithmetic updates should
    guard with a condition expression, and see transport_replay below for the
    layer this client does not control.
  transport_replay:
    what: >
      requirement:connection-reuse replays a request once, below this client,
      when a pooled connection fails before any response byte arrives and the
      body can be rebuilt. A DynamoDB body is a buffered JSON document, so
      http.NewRequest always populates GetBody and the precondition always holds.
    build_path_difference:
      tinygo: the transport replays, as above
      std_go: >
        net/http declines to replay a POST once its bytes were written, so the
        transport returns the error instead
      equalizer: >
        this client retries a transport failure itself, which is what makes the
        two paths behave the same. Measured both ways in
        TestPooledConnectionReplayDeliversTwice, which carries no build tag.
      consequence: >
        the hazard is not a tinygo quirk to be designed around, it is inherent
        to recovering a dead pooled connection at all
    consequence: >
      retries multiply. Three client attempts over a replaying transport can put
      the same write on the wire six times. The client cannot observe the
      replay, so it cannot count it.
    bound: >
      state the worst case, attempts x 2 deliveries, in the WithRetry
      documentation rather than trying to defeat it
    at_most_once: >
      not achievable here. A request can be delivered and its response lost on
      any connection, pooled or not, and the only DynamoDB idempotency token is
      on TransactWriteItems, which requirement:dynamodb-client-scope excludes.
    escape_hatch: >
      transport replay is gated on the connection having come from the pool, so
      Transport.DisableKeepAlives removes that layer specifically. It costs the
      handshake per call, which is the trade a caller doing arithmetic updates
      may want; see decision:dynamodb-connection-policy.
  batches: >
    unprocessed keys and items are returned, not retried. They mean partial
    success, so retrying them is the caller's decision about which writes still
    matter; see requirement:dynamodb-client-scope.
response_integrity:
  header: x-amz-crc32, present on success and error replies
  check: crc32.ChecksumIEEE over the raw body, compared before decoding
  mismatch: retried as a transport fault, then reported as ErrChecksumMismatch
  absent_header: >
    accepted. Both DynamoDB and DynamoDB Local send the header, verified on
    2026-07-31, so this tolerates a proxy that strips it rather than a server
    that omits it. Failing every request in that case would be the worse answer.
context: a cancelled or expired context stops retrying immediately and returns the context error
state: shipped 2026-07-31, 3 attempts, 25ms base, 1s cap, full jitter
acceptance:
  - a stub server returning 400 ThrottlingException twice then 200 succeeds in one call
  - a stub server returning ValidationException is not retried
  - a corrupted body with a valid crc32 header retries, then reports ErrChecksumMismatch
  - backoff respects a context deadline shorter than the retry budget
  - the same tests pass under go test -tags force_tinygo_logic
  - >
    a server that drops a pooled connection before responding delivers the
    request twice, asserted on both build paths so the hazard is pinned by a
    test rather than by prose
documented_in:
  godoc: >
    WithRetry carries the worst case, attempts x 2 deliveries, and UpdateItem
    carries the ADD warning. This is a correctness caveat, so it belongs where a
    caller meets the API, not only in a README section they may not read.
  nosql/dynamodb README: >
    a "Retries and idempotency" section: which errors are retried, that the
    transport replays underneath, and the condition-expression guard
  https README: >
    already states that a stale pooled connection replays the request; this
    client documents what that means for a write
related: decision:dynamodb-connection-policy
