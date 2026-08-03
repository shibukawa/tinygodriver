---
id: requirement:datastore-client-scope
type: requirement
title: Datastore Client Scope
---
`nosql/datastore` covers the eight data-API RPCs of system:google-datastore, with the credential and endpoint model of api:google-auth. It is the Datastore-side equivalent of requirement:dynamodb-client-scope, and the boundary differs where the wire differs.

```yaml
priority: must
state: proposed 2026-08-02
in_scope:
  entities:
    - Get, GetMulti over lookup
    - Put, Insert, Update, Delete, and their Multi forms, over commit
    - mutation preconditions; see decision:datastore-write-preconditions
    - AllocateIDs, because an incomplete key has to come from somewhere
  queries:
    - runQuery with kind, filter, order, projection, distinctOn, limit and offset
    - cursor pagination, one batch per call, moreResults surfaced to the caller
    - ancestor queries, which are a filter on the key path
    - EVENTUAL read consistency as an option, STRONG by default
  transactions:
    - read-write via RunInTransaction, with the ABORTED retry loop inside it
    - read-only transactions, for a consistent snapshot across several reads
    - single-use transactions where the shape allows, to save a round trip
  aggregation:
    - runAggregationQuery with COUNT
    - reason: >
        counting by paging through keys costs a read per entity, so leaving this
        out pushes callers toward the expensive thing
  addressing:
    - named databases through the request-level databaseId
    - namespaces on keys, since multi-tenant callers cannot add them later
  credentials: api:google-auth, service account or a supplied token
  endpoints:
    - "https://datastore.googleapis.com"
    - the emulator over http, see decision:datastore-emulator-endpoint
out_of_scope:
  gql:
    reason: a second request shape for the same queries, and it needs an escaping story
  reserve_ids:
    reason: an import and restore tool, not an application call
  aggregations_beyond_count:
    apis: SUM and AVG
    reason: >
      COUNT answers the question that otherwise costs a full scan; the other two
      are conveniences over data the caller can page
  admin_api:
    apis: index management, import, export, operations
    reason: a different host and a different permission model
  auto_pagination: >
    no iterator that hides round trips. endCursor feeds startCursor explicitly,
    the same shape as api:dynamodb-client and api:s3-client.
  watch_and_listeners:
    reason: Datastore mode has none; that is a Firestore native mode feature
  property_transformations:
    reason: >
      server-side increment and array-append exist on the wire but only inside
      commit, and they reintroduce exactly the non-idempotent-retry hazard
      requirement:datastore-retry-policy is built to avoid
  explain_options: a query-planning diagnostic, not a runtime need
acceptance:
  - "unit tests pass under go test and go test -tags force_tinygo_logic"
  - >
    examples/datastoredemo, built with tinygo 0.41.1, runs a put, a get, a
    keyed and an ancestor query over two pages, a transaction, and a delete
    against the emulator
  - a multi-byte string, a blob, a nested entity, a timestamp and an empty string round-trip
  - an int64 beyond float64 precision survives a round trip as text
  - an insert against an existing key surfaces ErrAlreadyExists, not a generic 409
  - a transaction losing a race retries and then succeeds, asserted against a stub
  - sequential calls on one client reuse one connection, counted against a local server
  - a build using only a supplied token does not link crypto/x509, checked with tinygo build size
decided_by: decision:no-cloud-google-go-datastore
surface: api:datastore-client
differences: concept:dynamodb-datastore-mapping
retry: requirement:datastore-retry-policy
