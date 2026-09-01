---
id: rule:tinygo-test-constraints
type: rule
title: Writing Tests That Run Under tinygo test
---
Two TinyGo facts break tests written the ordinary way, both silently enough to look like a bug in the code under test.

```yaml
scope: any package whose suite is meant to run under `tinygo test`
constraints:
  fatal_does_not_stop:
    what: >
      t.Fatalf prints "FailNow is incomplete, requires runtime.Goexit()" and
      execution continues. The next line runs with whatever the failed step
      returned, so a nil conn becomes a nil pointer dereference and the real
      failure is buried under a panic
    rule: >
      follow every Fatalf with an explicit return, and have helpers report
      failure through a second result rather than by not returning
  listener_addr_port_zero:
    what: >
      net.Listener.Addr() reports port 0 for a port 0 listen under
      system:tinygo-netdev, so a test cannot ask the listener what it got and a
      dial to that address fails with "can't assign requested address"
    rule: >
      choose the port in the test, from a counter, and retry on conflict; return
      the chosen address rather than reading it back
known_intermittent:
  websocket_TestConcurrentClients:
    what: >
      16 clients x 50 echo round trips over real netdev sockets hangs forever,
      intermittently, under `tinygo test ./websocket` on darwin/arm64 with
      tinygo 0.42.0 (scheduler threads, the host default)
    rate: >
      measured 2026-09-02 with a 150 s watchdog: 2 of 6 runs on a pristine
      checkout, 4 of 6 on a tree whose websocket change touched no code this
      test executes. Treat the two as the same rate at that sample size
    stack: >
      `sample` of a hung binary shows every thread either parked in
      internal/task.Pause (__ulock_wait) or blocked in the listener's Accept;
      nothing is runnable. A lost wakeup between netdev and the threads
      scheduler, not a test bug: no deadline is missing, the goroutines are
      simply never resumed
    not_a_regression_of: the fork's patch retirement, decision:websocket-fork
    scheduler_tasks_is_not_an_escape: >
      -scheduler=tasks fails to link on the darwin host at 0.42 with
      "duplicate symbol: _tinygo_task_exit", and rule:tinygo-threads-scheduler
      already rules it out for netdev work
    how_to_recognise: >
      `tinygo test ./websocket` that never prints a result; the last
      "=== RUN" line is TestConcurrentClients. Go ignores SIGALRM, so a
      perl/alarm deadline does not stop it; kill the test binary itself
    watch_out: >
      two concurrent `tinygo test` runs of any package here collide on the
      fixed port ranges (19700, 19800, 19900) and fail spuriously. Run them one
      at a time
supported: t.Helper, t.Cleanup, t.Skip and subtests all work
verify: >
  a suite that passes under `go test` and `go test -tags force_tinygo_logic`
  but panics under `tinygo test` is almost always one of these two, not a
  platform bug
precedent: httpserver/httpserver_test.go, websocket/integration_test.go
```
