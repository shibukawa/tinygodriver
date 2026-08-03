---
id: rule:mysql-multi-statement-batch
type: rule
title: MySQL Multi-Statement Batch Constraints
---
What the mysql adapter of decision:batch-transport-per-driver must enforce, because multiStatements changes connection semantics that the caller configured in the DSN.

```yaml
required_dsn_flags:
  multiStatements: >
    true. The capability is negotiated at handshake, so it cannot be turned on
    per batch. A connection without it rejects the joined statement at the server
  interpolateParams: >
    true whenever a queued statement carries arguments. mysqlConn.Exec returns
    driver.ErrSkip with args and interpolation off, and the prepared-statement
    fallback cannot accept multiple statements
  detection: >
    neither flag is readable from a driver.Conn, so the adapter discovers them by
    failing. Translate the server error and driver.ErrSkip into one message naming
    both flags, rather than surfacing ErrSkip to the caller
security_posture:
  multi_statements: >
    an injection that reaches the sql text can now append a second statement.
    This is a real widening of the blast radius and belongs in the package doc,
    not only here
  interpolate_params: >
    the driver escapes values itself instead of sending them out of band. Its
    escaping is correct, but it refuses unsafe collations and rejects argument
    types it cannot render, returning ErrSkip
  mitigation: >
    the flags are the caller's DSN choice. The batch api must never rewrite the
    DSN or open a second connection to obtain them
size_ceiling:
  mechanism: interpolateParams compares the rendered statement against maxAllowedPacket
  failure_mode: driver.ErrSkip, indistinguishable from the flag-off case without extra state
  duty: track the rendered length and report an oversized batch as its own error
atomicity:
  default: wrap the joined statement in START TRANSACTION and COMMIT, per requirement:sql-batch-execution
  ddl: >
    DDL forces an implicit commit in mysql, so a batch containing DDL is not
    atomic no matter what wraps it. Say so rather than implying protection
error_attribution:
  server_behavior: execution stops at the first failing statement; later statements never run
  visible_signal: >
    the count of consumed result sets, or the length of AllRowsAffected on a
    successful Result. On error the driver returns no Result, so the index is
    often unknown
  duty: report the index when it is known and state that it is unknown when it is not
result_reading:
  exec: driver.Result asserted to AllRowsAffected and AllLastInsertIds, one entry per statement
  query: >
    phase p2. Either driver.Rows walked with driver.RowsNextResultSet inside Raw,
    or sql.Rows.NextResultSet outside it; see rows_layer in
    decision:batch-transport-per-driver
  empty_statements: >
    a trailing ";" or a blank queued statement produces a result set the caller
    never queued; reject empty entries at Queue time
tinygo:
  verified: multiStatements works under tinygo, per the mysql driver validation memory
  unverified: batching over tls, and batching against MariaDB as opposed to MySQL
```
