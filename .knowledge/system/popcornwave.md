---
id: system:popcornwave
type: system
title: Popcorn Wave Framework
---
A web framework building its authentication and session backends on `nosql/datastore`, through the `firestorebind` code generator in system:tinybind-go. It is the first framework-scale consumer of this driver, and the shape of its workload is what makes requirement:datastore-single-use-transaction worth doing.

```yaml
import_path: github.com/shibukawa/popcornwave
pins: tinygodriver v1.1.6
why_not_earlier: >
  v1.1.4 and v1.1.5 write key properties without a partition, fixed on
  2026-08-04. A store keeping a key as a property on those tags writes data it
  cannot read back consistently; see keys_in_values_bug in data:datastore-value.
stores:
  count: five, all framework-owned
  what: session records, ceremony state, an OIDC allowlist, passkey credentials, bootstrap credentials
  selection: one auth.backend and session.backend configuration value
workload_shape:
  every_conditional_write_is_a_transaction: >
    the wire has no condition expression, so a predicate over a stored value has
    to run in Go between a read and a commit; see
    decision:datastore-write-preconditions
  and_every_one_is_one_read_plus_one_commit:
    - "ceremony Take: read, delete, return the prior entity, which no commit result carries"
    - "ceremony Put over an expired collision: replace only if the stored deadline passed"
    - "passkey assertion: write only if the accepted sign count exceeds the stored one"
    - "bootstrap spend: decrement only if attempts remain and the record is unconsumed"
    - "first passkey enrollment: spend the secret and store the credential as one unit"
  consequence: >
    three round trips each where the wire allows two, on the login path. That is
    the exact shape the wire's fold was designed for, which is why the ask is
    for the narrow version and not a general change.
does_not_need:
  - a composite index; every read is a key lookup, a batch lookup, or a single-property equality query
  - the admin API, for indexes or TTL; operational configuration is deployment tooling's
  - property transformations; the two arithmetic-shaped operations go through transactions
  - a portable facade over nosql/dynamodb and nosql/datastore, which both catalogs decline
requests_2026_08_05:
  against_this_repository:
    - requirement:datastore-single-use-transaction
    - requirement:datastore-doc-accuracy
    - requirement:datastore-read-time-bound
  against_tinybind_go: >
    four more, on firestorebind: a keys-taking batch delete, using
    Client.MutationSize instead of a local overhead constant, exporting the
    namespace application, and a declaration-only ttl tag. Recorded here only so
    the split is visible; they are not this repository's to answer.
  what_the_second_of_those_produced: >
    wiring MutationSize into firestorebind is what found the commit envelope,
    since a measure that covers the mutations and not the request around them
    cannot replace an overhead constant on its own. Answered by
    requirement:datastore-commit-envelope.
