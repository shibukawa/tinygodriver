---
id: requirement:datastore-commit-envelope
type: requirement
title: A Caller Chunking A Commit Must Be Able To Size The Whole Request
---
`Client.MutationSize` measures a mutation, and a commit is more than its mutations: a mode, an optional databaseId, an optional transaction handle or singleUseTransaction block, and the array holding them. A caller chunking against MaxRequestBytes by summing MutationSize undercounts the request it is about to send.

```yaml
priority: should
state: shipped 2026-08-05
requested_by: system:tinybind-go
severity: low
why_low_but_still_real: >
  1042 bytes against a 10 MiB limit is 0.01%, and a rejected commit needs a
  batch landing within about a kilobyte of exactly MaxRequestBytes. It is
  filed because it was the one figure that stayed unknown to a caller after
  MutationSize shipped, and a 400 from the service is a poor way to learn it.
measured:
  method: >
    summing MutationSize against the bytes a stub server received at :commit,
    upserting entities with a 64-byte string property
  non_transactional: envelope = 42 + n, where n is the mutation count
  empty_body: >
    43 bytes, {"mode":"NON_TRANSACTIONAL","mutations":[]}, and n mutations add
    n-1 commas, which is the same 42 + n
  named_database: >
    fixed part 42 to 75 with WithDatabase("my-named-database"), the 33 bytes of
    "databaseId":"my-named-database",
  transactional_single_use: >
    78 + n, the mode being TRANSACTIONAL and singleUseTransaction carrying
    {"readWrite":{}} or {"readOnly":{}}
  transactional_handle: >
    variable, since the handle is server-chosen base64 and only exists after a
    read has returned one
  reporter_figures_reproduced: 2026-08-05, exactly, at v1.1.7
the_database_is_counted_twice: >
  and both are real. Every key carries a full partitionId, per the partition
  entry in data:datastore-value, so a named database is inside each mutation and
  MutationSize already sees it. The request carries it once more at the top
  level, where MutationSize cannot.
surface:
  client: >
    CommitOverheadBytes(n int) int, the non-transactional envelope
  tx: >
    the same on Tx, which is where the transactional figure has to live: the
    commit carries a handle or a singleUseTransaction block, and only the
    transaction knows which. This is the argument that made MutationSize a
    Client method rather than an Entity function, applied one level in.
  takes_a_count_not_the_mutations: >
    so chunking stays a running total. The caller's question is "given a sum
    and a count, will one more fit", and a whole-batch CommitSize would
    re-measure everything on every step. The reporter named this and was right.
  no_error_return: >
    marshalling a mode string and an empty slice cannot fail, and an error
    return would put a branch in a loop that has no way to take it.
implemented:
  where: commitOverhead marshals the real wireCommitRequest with no mutations
  why_not_a_constant: >
    a constant is how the caller got the wrong number in the first place. A
    field added to the wire shape is counted here without anyone remembering
    this function exists.
acceptance:
  - CommitOverheadBytes(len(ms)) plus MutationSize over ms equals the commit body, for 1, 2, 10, 100 and 1000 mutations
  - the same holds with a named database, and the difference is the databaseId counted once
  - the same holds inside a transaction that wrote without reading, which commits with singleUseTransaction
  - the same holds inside a transaction that read first, which commits against the handle
  - the tests fail if the per-mutation comma is miscounted
  - "unit tests pass under go test and go test -tags force_tinygo_logic"
consumes: system:google-datastore
surface_concept: api:datastore-client
sizing_companion: requirement:datastore-client-scope
