---
id: requirement:datastore-single-use-transaction
type: requirement
title: Fold beginTransaction Into The First Call
---
A transaction that performs one read and commits once must cost two round trips, not three. The wire carries `readOptions.newTransaction` and `CommitRequest.singleUseTransaction` for exactly that, and this client uses neither.

```yaml
priority: must
state: proposed 2026-08-05
requested_by: system:popcornwave
current_behaviour:
  every_transaction_begins_explicitly: >
    runOneTransaction calls beginTransaction, then the closure's reads, then
    commit. Three round trips, always.
  wire_members_present_but_unused:
    SingleUseTransaction: declared on wireCommitRequest and never assigned
    newTransaction: not declared on wireReadOptions at all, so the read half is not expressible
  verified: 2026-08-05, against the code at v1.1.6
why_it_matters:
  not_a_micro_optimisation: >
    Datastore has no condition expression, so decision:datastore-write-preconditions
    routes every predicate over a stored value through a transaction. For
    system:popcornwave that is five stores whose conditional writes are all one
    read plus one commit, on the login path. The saved round trip is one third
    of every one of them.
  the_shape_is_the_designed_one: >
    the fold exists for exactly "one read, then commit". This is not asking the
    wire to do something unusual.
scope:
  in:
    - a read-write transaction whose closure performed exactly one read and queued mutations
    - the read-only equivalent, where a single read needs no separate begin
  out:
    - folding a transaction with more than one read; the second read needs the handle the first returned
    - any change to RunInTransaction's contract, retry behaviour or closure semantics
  fallback: >
    a closure that reads twice, or reads after queueing, takes the explicit
    begin unchanged. The fold is an optimisation of a recognised shape, never a
    restriction on what a closure may do.
design_note:
  the_hard_part: >
    the client cannot know the shape until the closure has run, and the first
    read has to go out before that. So the fold is decided at the first read:
    send newTransaction, and keep the handle the reply carries for the commit.
    A closure that then reads again simply uses that handle, which is the
    ordinary path with the begin already paid for.
  reply_carries_the_handle: >
    a lookup or runQuery answering a newTransaction request returns the
    transaction in its reply. That is what makes this a fold rather than a
    guess: the client is not committing to a shape, it is starting the
    transaction inside the read it was going to send anyway.
  commit_side: >
    singleUseTransaction covers a commit with no read at all, which is a
    different shape and not one system:popcornwave has. Wire it or leave the
    field unset, but do not leave it declared and unassigned as it is now.
acceptance:
  - a closure with one read and queued mutations sends two requests, not three
  - the first request carries readOptions.newTransaction and no transaction handle
  - the commit carries the handle the read's reply returned
  - a closure with two reads sends the second with the handle from the first, and still commits once
  - "ABORTED still re-runs the whole closure, and the re-run folds again"
  - a closure that returns an error rolls back the transaction the read started
  - a read-only transaction over one read sends two requests
  - the same tests pass under go test -tags force_tinygo_logic
consequences:
  - >
    the rollback path gains a case: a transaction started inside a read must
    still be rolled back if the closure fails, and the handle for it comes from
    the reply rather than from a begin the client issued
  - >
    a stub-server test can no longer assume the first request is beginTransaction,
    which several existing transaction tests do
corrects: decision:datastore-write-preconditions
surface: api:datastore-client
wire_reference: system:google-datastore
