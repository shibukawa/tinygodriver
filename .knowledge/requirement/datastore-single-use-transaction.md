---
id: requirement:datastore-single-use-transaction
type: requirement
title: Fold beginTransaction Into The First Call
---
A transaction that performs one read and commits once must cost two round trips, not three. The wire carries `readOptions.newTransaction` and `CommitRequest.singleUseTransaction` for exactly that, and this client uses neither.

```yaml
priority: must
state: shipped 2026-08-05
measured:
  one_read_then_commit: 2 round trips, was 3
  n_reads_then_commit: n+1, was n+2
  write_only: 1, was 3
  neither_read_nor_wrote: 0, was 2
  method: counting the operations a stub server received, per shape
requested_by: system:popcornwave
behaviour_before:
  every_transaction_begins_explicitly: >
    runOneTransaction called beginTransaction, then the closure's reads, then
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
    - any change to RunInTransaction's contract, retry behaviour or closure semantics
  every_shape_improved: >
    the ask was scoped to one read plus one commit, on the assumption that
    anything else would keep the explicit begin. Starting the transaction inside
    the first call that needs one removes it everywhere instead: a second read
    uses the handle the first brought back, so N reads cost N+1 rather than N+2,
    and nothing restricts what a closure may do.
implemented:
  where: Tx starts lazily; buildReadOptions and commit decide from its state
  read_side: readOptions.newTransaction on the first read, transaction thereafter
  commit_side: >
    singleUseTransaction when no read started one, which turned out to matter
    more than expected: a write-only closure went from three round trips to one,
    and that shape was not in the original ask
  rollback: only when a handle exists, since a closure that never started one has nothing to release
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
