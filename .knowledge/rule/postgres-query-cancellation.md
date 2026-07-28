---
id: rule:postgres-query-cancellation
type: rule
title: Cancel Queries with CancelRequest, Never with SetDeadline
---
Context cancellation on the TinyGo path must send the protocol-level CancelRequest on a second connection; changing a deadline on the in-flight connection is silently ineffective.

```yaml
scope: the tinygo path of decision:postgres-backend-split
defect:
  observed: >
    pgx defaults to DeadlineContextWatcherHandler, which calls SetDeadline(now)
    from a watcher goroutine. Under tinygo a 500ms context on SELECT pg_sleep(3)
    ran the full 3s and returned err=nil
  severity: silent wrong behavior, not a build or runtime error
  root_cause: >
    tinygo net.TCPConn.SetDeadline only assigns a field; system:tinygo-netdev
    waitFD snapshots the deadline when Recv is entered and blocks in select(),
    so a later deadline change cannot interrupt the blocked read
  minimal_repro:
    host_go: blocked Read interrupted 300ms after a late SetDeadline, i/o timeout
    tinygo: blocked Read not interrupted, still blocked after 2s
fix:
  requires_no_source_change: true
  how: >
    set Config.BuildContextWatcherHandler to pgconn.CancelRequestContextWatcherHandler;
    pgconn/ctxwatch is a public package, not internal
  measured:
    host_go: 616ms, SQLSTATE 57014
    tinygo: 612ms, SQLSTATE 57014
  note: the handler also sets a fallback deadline, harmless but ineffective here
rules:
  - the tinygo path must install the CancelRequest handler explicitly; the pgx
    default is wrong on this platform
  - a deadline set before a read starts is honored, so connect timeouts are fine
  - a deadline changed after a read has started must not be relied on
  - requires rule:tinygo-threads-scheduler, since the handler cancels from a goroutine
acceptance: >
  a context with a 500ms timeout aborts SELECT pg_sleep(3) in under 1s and
  returns SQLSTATE 57014, on both build tags
precedent: >
  lib/pq cancels correctly under tinygo for the same reason, it always used a
  separate connection; measured 505ms and 57014
alternative_rejected:
  fix_netdev: >
    making waitFD re-check a mutable deadline would fix the whole net layer, but
    widens scope beyond requirement:tinygo-postgres-driver
