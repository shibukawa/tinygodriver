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
supported: t.Helper, t.Cleanup, t.Skip and subtests all work
verify: >
  a suite that passes under `go test` and `go test -tags force_tinygo_logic`
  but panics under `tinygo test` is almost always one of these two, not a
  platform bug
precedent: httpserver/httpserver_test.go, websocket/integration_test.go
```
