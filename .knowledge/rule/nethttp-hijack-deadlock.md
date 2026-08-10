---
id: rule:nethttp-hijack-deadlock
type: rule
title: net/http Hijack Deadlocks on netdev
---
`http.ResponseWriter.Hijack` never returns under TinyGo, so no protocol upgrade can be served from `http.Server`. Same root cause as rule:postgres-query-cancellation: a deadline changed after a read has started is ineffective on system:tinygo-netdev.

```yaml
scope: every TinyGo user of net/http Server; websocket, h2c, CONNECT tunnelling
defect:
  observed: >
    client sends the handshake, server logs "is Hijacker: true", then nothing.
    Client times out. No error, no panic, no log line
  severity: silent hang, not a build or runtime error
  root_cause: >
    serve() calls startBackgroundRead() before every bodyless-GET handler
    (tinygo net/http server.go:1987). hijackLocked() then calls
    abortPendingRead(), which sets the read deadline into the past and blocks on
    cond.Wait() until the background read notices. It never notices: netdev
    waitFD returns immediately for a zero deadline and sysRecv then blocks in a
    plain recv(), and Netdever takes the deadline by value at call time
  differs_from_netdev_note: >
    system:tinygo-netdev records waitFD snapshotting the deadline and blocking
    in select(); the zero-deadline case skips select() entirely and blocks in
    recv(). Same class, different line
  minimal_repro: >
    hijack a bodyless GET under tinygo with netdev registered; Hijack does not
    return
positive_control: >
    deliver one byte AFTER the request is parsed, not pipelined, and the
    background read completes on its own; abortPendingRead then has nothing to
    wait for, Hijack returns and every downstream write works. Pipelining the
    byte does not work, bufio consumes it before the background read starts
not_fixable_in_netdev: >
  Netdever passes deadline by value per call, so no netdev change can interrupt
  a call already in flight. A fix belongs in tinygo net (make the deadline live,
  or poll a non-blocking socket) or tinygo net/http (drop the background read).
  Same conclusion as the fix_netdev alternative rejected in
  rule:postgres-query-cancellation, for the same reason
rules:
  - do not call Hijack from a handler served by http.Server on the tinygo path
  - route upgrade requests around http.Server, see decision:httpserver-demux
  - a handler that reaches Hijack anyway must fail loudly, never hang
  - on the fasthttp stack the rule does not apply at all; see fasthttp_hijack
fasthttp_hijack: >
  fasthttp's RequestCtx.Hijack has no equivalent defect and needs no
  demultiplexer. fasthttp finishes the response, stops reading, and calls the
  handler on the same goroutine, so there is no background read for anything to
  abort. Verified 2026-08-10 by requirement:fasthttp-websocket-fork, whose whole
  battery runs over real sockets under tinygo test rather than reading the code.
  So there are two routes to a working upgrade: websocket + httpserver for
  net/http applications, decision:fasthttp-websocket-vendoring for fasthttp ones.
verify: >
  build a hijacking handler under tinygo and confirm the handshake completes;
  a regression reappears as a client timeout, so the test needs its own deadline
measured: 2026-08-10, tinygo 0.41.1 darwin/arm64, by requirement:websocket-fork
```
