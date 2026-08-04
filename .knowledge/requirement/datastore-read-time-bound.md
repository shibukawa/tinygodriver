---
id: requirement:datastore-read-time-bound
type: requirement
title: WithReadTime Must State What A Legal Read Time Is
---
`WithReadTime` accepts any instant and documents no bound, so the only way to learn which ones the service accepts is to send one and be refused. Worse, the option's own encoding produces a value the service rejects for every read older than an hour.

```yaml
priority: should
state: proposed 2026-08-05
requested_by: system:popcornwave
current_behaviour:
  godoc: "WithReadTime reads the database as of a past instant."
  encoding: 'o.at.UTC().Format(time.RFC3339Nano)'
  verified: 2026-08-05, against the code at v1.1.6
the_service_rule:
  source: the published point-in-time recovery documentation, read 2026-08-05
  within_the_past_hour: >
    any microsecond-granularity timestamp, whether or not PITR is enabled, but
    not before the database's earliestVersionTime
  one_hour_to_seven_days: >
    whole-minute timestamps only, and only with PITR enabled. A sub-minute
    timestamp in this range is refused as read_time too old, which names the
    wrong cause: the instant is in range and the precision is not.
  beyond_that: refused
the_encoding_hazard:
  what: >
    RFC3339Nano keeps sub-minute precision, so the obvious call —
    WithReadTime(time.Now().Add(-2*time.Hour)) — is refused, and the message
    blames the age rather than the precision.
  reading: >
    this is more than a missing doc line. The option encodes a value that is
    correct for the recent case and wrong for the older one, and nothing tells
    the caller which case they are in.
what_to_do:
  document:
    - the one-hour microsecond window and the seven-day whole-minute window
    - that the seven-day window needs PITR enabled on the database
    - that earliestVersionTime can be later than either bound on a young database
    - that a read older than an hour must be truncated to a whole minute
  do_not_enforce: >
    the client cannot see whether PITR is enabled or what earliestVersionTime
    is, so a client-side range check would refuse reads that work. Same position
    as MaxDisjunctions: state the bound, let the service enforce it.
  truncation:
    chosen: do not truncate silently
    reason: >
      truncating changes the instant the caller asked for, and a caller reading
      a specific moment would get a different one with no signal. Truncating is
      the caller's decision because only the caller knows whether the exact
      instant mattered.
    alternative_considered: >
      truncate when the time is more than an hour old, since sub-minute
      precision is unusable there anyway. Rejected as too clever: the boundary
      moves as the request travels, so the same call could truncate or not
      depending on latency.
acceptance:
  - WithReadTime's godoc states both windows, the PITR condition, and the truncation obligation
  - the nosql/datastore README says the same where read options are described
  - no client-side range or granularity check is added
nothing_depends_on_it_yet: >
  filed by system:popcornwave before a store reaches for it, because the failure
  is an INVALID_ARGUMENT on a value that looks entirely reasonable
surface: api:datastore-client
wire_reference: system:google-datastore
