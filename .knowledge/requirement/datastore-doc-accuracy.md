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
  catalog_out_of_scope:
    where: requirement:datastore-client-scope, found 2026-08-05 by system:tinybind-go
    says: SUM and AVG are out of scope, for the reason the same file names as backwards
    reality: >
      shipped 2026-08-04, twenty lines above, in the entry that records the
      reversal. The file stated the correction and the corrected-away claim
      both as current.
    why_it_survived_the_fix: >
      this requirement fixed the README's list and cited
      requirement:datastore-client-scope as the authority for why that list was
      wrong, while that file was carrying the identical error. The guard written
      the same day read README.md alone.
    also_found_there:
      - >
        single_use_state read as unimplemented after the fold shipped. True of
        v1.1.6 as written, but a scope document is read as current state.
      - >
        state was "proposed 2026-08-02" through five tags in which every line of
        in_scope landed. Not reported; found while fixing the other two, in the
        header of the file the report was about.
the_pattern:
  rounds:
    - four stale entries in api:datastore-client, found downstream
    - MarshalEntity in data:datastore-value, found downstream
    - a Mutate variadic in api:datastore-client, found by the audit test the day it was written
    - "2026-08-05: the README, and the single_use claim"
    - >
      2026-08-05, the same day: the same SUM and AVG claim in
      requirement:datastore-client-scope, which the README's fix had cited as
      its authority. A guard aimed at one artifact says nothing about the
      identical claim in the next.
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
      to the not-in-scope lists: a name in one that is also an exported
      identifier is a contradiction.
    every_such_list_not_one: >
      the first version read README.md alone and missed the identical claim in
      requirement:datastore-client-scope for a further tag. TestNotInScope-
      NamesNothingExported now reads the README section and the out_of_scope
      block of every Datastore concept in the catalog.
    limit: >
      prose still cannot be checked. This catches the list-shaped claims, which
      are the ones that have actually been wrong, and says nothing about the
      paragraphs around them.
    scoped_two_ways_after_measuring: >
      the unfiltered sweep produced two false positives, and both taught
      something. It read requirement:sql-batch-execution's out_of_scope, where
      INSERT is SQL and collides with this package's Insert by coincidence — a
      concept about another package cannot make a claim about this one's
      surface, so only Datastore concepts are read. And it objected to COUNT
      inside a reason explaining why the other aggregations were excluded, which
      is prose naming an in-scope feature to contrast with it, so reasons are
      dropped and the keys and their apis values are what gets read.
    do_not: >
      try to check every sentence. A check that fails on a rewording trains
      people to edit around it, which is worse than not having it. The two
      filters above exist because the unfiltered version was already doing that
      on its first run.
acceptance:
  - no not-in-scope list names anything the package exports, in the README or in the catalog
  - a test fails when one does, and names the file
  - the test passes with no false positive on a catalog that is correct
  - SkippedResults, DistinctOn, Project and RunReadOnly appear in the README
  - decision:datastore-write-preconditions no longer claims the fold is implemented
  - requirement:datastore-client-scope's out_of_scope no longer names SUM or AVG
  - the audit test still passes on both build paths
state_2026_08_05: >
  all instances above fixed and the guard extended. The class is not closed:
  the mechanism now covers list-shaped claims in two artifact kinds, and every
  round so far has found the next instance in an artifact the previous round's
  guard did not read.
related: system:popcornwave
also_reported_by: system:tinybind-go
