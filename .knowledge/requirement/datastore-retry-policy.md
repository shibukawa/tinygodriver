---
id: requirement:datastore-retry-policy
type: requirement
title: Retry, Contention And Transaction Restart
---
Datastore expects the client to retry, but the retryable case is contention rather than throttling, and the unit of retry is sometimes a whole transaction rather than a request.

```yaml
priority: must
reason: >
  ABORTED on a contended entity group is normal operation, the way
  ProvisionedThroughputExceededException is for a provisioned DynamoDB table.
  Unlike that case, retrying the failed request alone is wrong: the reads the
  transaction was built on are stale, so the closure has to run again.
classification:
  source: the status string in the error body, never the HTTP code
  why: >
    ABORTED and ALREADY_EXISTS are both 409, one retryable and one terminal.
    Matching on 409 would retry a duplicate insert forever.
  retryable: [UNAVAILABLE, DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED]
  once_only:
    INTERNAL: >
      Google documents "do not retry more than once". One extra attempt, not the
      full budget.
  transaction_scope:
    ABORTED: >
      retryable, but by re-running the transaction closure, not by resending the
      commit. Outside a transaction it is terminal.
  terminal: [INVALID_ARGUMENT, FAILED_PRECONDITION, NOT_FOUND, ALREADY_EXISTS, PERMISSION_DENIED, UNAUTHENTICATED]
  unauthenticated_hint: >
    the likeliest cause on an embedded target is a wrong clock, not a wrong key;
    see decision:google-token-strategy
policy:
  request: 3 attempts, exponential backoff with full jitter, 25ms base, 1s cap
  transaction: 3 closure re-runs on ABORTED, with the same backoff, budgeted separately
  bound: >
    total elapsed never exceeds one operation timeout. One context is derived
    before the first attempt, so token refresh, retries, backoff, and body reads
    all consume the same budget, including with a supplied HTTP client.
  option: WithRetry(attempts int, base time.Duration); zero disables it
  context: a cancelled or expired context stops retrying and returns the context error
idempotency:
  insert_update_delete: >
    idempotent by construction. Insert repeats as ALREADY_EXISTS, delete of an
    absent key succeeds, and update replaces rather than accumulates. This is
    the one place Datastore is easier than DynamoDB, whose ADD update is not
    replayable; see concept:dynamodb-datastore-mapping.
  guarded_by_scope: >
    property transformations would break that and are excluded in
    requirement:datastore-client-scope for this reason
  transport_replay:
    what: >
      requirement:connection-reuse replays a request once, below this client,
      when a pooled connection dies before any response byte arrives. Every
      Datastore body is a buffered JSON document, so GetBody is always set and
      the precondition always holds.
    consequence: >
      attempts x 2 deliveries in the worst case, the same arithmetic as
      requirement:dynamodb-retry-policy. It is harmless for the mutation verbs
      above and it is not harmless for a commit carrying a transaction handle,
      which the server rejects on the second delivery.
    commit_replay: >
      a replayed transactional commit fails rather than double-writing, because
      the transaction handle is consumed. That failure is reported, not retried.
    build_path_difference:
      tinygo: the transport replays
      std_go: net/http declines to replay a POST once written, so the error surfaces
      equalizer: this client retries transport failures itself, so both paths behave alike
no_response_integrity: >
  there is no x-amz-crc32 equivalent, so the checksum layer of
  requirement:dynamodb-retry-policy has nothing to map onto. TLS is the only
  integrity guarantee on this path, which is a real difference and not an
  oversight.
token_refresh:
  what: a 401 UNAUTHENTICATED with a valid cached token means the token expired early
  policy: >
    refresh once and resend, then give up. This is a retry the request-level
    budget does not cover, because the request was not at fault.
acceptance:
  - a stub returning UNAVAILABLE twice then 200 succeeds in one call
  - a stub returning INVALID_ARGUMENT is not retried
  - a stub returning ALREADY_EXISTS is not retried, despite being a 409
  - a stub returning INTERNAL is retried exactly once
  - a transaction whose commit returns ABORTED re-runs the closure, and the reads run again
  - backoff respects a context deadline shorter than the retry budget
  - a 401 refreshes the token once and resends
  - the same tests pass under go test -tags force_tinygo_logic
documented_in:
  godoc: >
    RunInTransaction carries the closure-runs-more-than-once warning, and
    WithRetry carries the attempts x 2 worst case
  nosql/datastore README: a "Retries and contention" section naming ABORTED as the expected case
wire_reference: system:google-datastore
counterpart: requirement:dynamodb-retry-policy
