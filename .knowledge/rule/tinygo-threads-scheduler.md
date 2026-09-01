---
id: rule:tinygo-threads-scheduler
type: rule
title: TinyGo Database Builds Require the Threads Scheduler
---
Any TinyGo build using system:tinygo-netdev for database work must use `-scheduler=threads`; the cooperative `tasks` scheduler deadlocks on blocking socket calls.

```yaml
scope: requirement:tinygo-postgres-driver, and any netdev user with background goroutines
measured:
  default_darwin_arm64: threads, full lib/pq suite passes
  scheduler_tasks:
    - context cancellation fails the same way as rule:postgres-query-cancellation
    - pq.Listener hangs indefinitely, killed after 60s
  cause: >
    a blocking cgo recv() holds the cooperative scheduler, so watcher and
    listener goroutines never run
rules:
  - document -scheduler=threads in the package readme and examples
  - do not rely on a goroutine making progress while another blocks in netdev
  - drive per-call timeouts from the deadline set before the read, not from a
    concurrent goroutine
verify: >
  build the driver test program with -scheduler=tasks and confirm it is a known
  unsupported configuration rather than a silent regression
measured_2026_09_02: >
  on tinygo 0.42.0 darwin/arm64, -scheduler=tasks no longer even links a host
  test binary: ld.lld reports "duplicate symbol: _tinygo_task_exit". So the
  configuration is unsupported at link time now, which is the loud failure
  this rule asked for
