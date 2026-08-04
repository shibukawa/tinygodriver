---
id: requirement:datastore-doc-accuracy
type: requirement
title: Shipped Documentation Must Not Contradict Shipped Code
---
The README's not-in-scope list still excludes SUM and AVG, which the same README documents two hundred lines earlier. This is the fourth round of the same defect and the second artifact it has appeared in, so the requirement is about the class, not the instance.

```yaml
priority: must
state: proposed 2026-08-05
requested_by: system:popcornwave
instances:
  readme_not_in_scope:
    says: "GQL, reserveIds, SUM and AVG aggregations, the admin API, auto-pagination, listeners"
    reality: Sum and Avg shipped 2026-08-04 with the reasoning for including them
    cost: >
      a consumer reading that list writes a paging loop to sum a property the
      service will sum — the exact outcome the sum_and_avg argument in
      requirement:datastore-client-scope was made to prevent
  readme_omissions:
    unmentioned_but_implemented: [SkippedResults, DistinctOn, Project, RunReadOnly]
    verified: 2026-08-05, zero occurrences of each in nosql/datastore/README.md
  concept_single_use:
    where: decision:datastore-write-preconditions
    says: "the client uses [the fold] whenever the transaction is one read plus one commit"
    reality: it does not; see requirement:datastore-single-use-transaction
    note: >
      the reporter quoted this as the intended behaviour, which was generous.
      It is written as a description of what the client does.
the_pattern:
  rounds:
    - four stale entries in api:datastore-client, found downstream
    - MarshalEntity in data:datastore-value, found downstream
    - a Mutate variadic in api:datastore-client, found by the audit test the day it was written
    - this round: the README, and the single_use claim
  cause_is_constant: >
    a design written before the code, or a code change made after the prose, and
    nobody re-reads the other one. Neither direction is caught by anything that
    fails.
  why_it_keeps_landing_on_consumers: >
    the .knowledge catalog and the README both ship inside the tag, so a stale
    line is released documentation. A consumer has no way to tell a description
    from an intention.
what_to_do:
  fix_the_instances: >
    remove SUM and AVG from the not-in-scope list, document the four omitted
    features, and mark the single_use claim as not implemented, pointing at
    requirement:datastore-single-use-transaction
  extend_the_mechanism:
    what: >
      TestConceptMatchesTheCode already checks that every client method the
      concept lists exists with the right variadic shape. Extend the same idea
      to the README's not-in-scope list: a name in it that is also an exported
      identifier is a contradiction.
    limit: >
      prose still cannot be checked. This catches the list-shaped claims, which
      are the ones that have actually been wrong, and says nothing about the
      paragraphs around them.
    do_not: >
      try to check every sentence. A check that fails on a rewording trains
      people to edit around it, which is worse than not having it.
acceptance:
  - the not-in-scope list names nothing the package exports
  - a test fails when it does
  - SkippedResults, DistinctOn, Project and RunReadOnly appear in the README
  - decision:datastore-write-preconditions no longer claims the fold is implemented
  - the audit test still passes on both build paths
related: system:popcornwave
