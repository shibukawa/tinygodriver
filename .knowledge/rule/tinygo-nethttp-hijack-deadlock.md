---
id: rule:tinygo-nethttp-hijack-deadlock
type: rule
title: net/http Hijack Deadlocks on netdev
---
Never build a protocol upgrade — WebSocket, CONNECT tunnelling, anything — on `net/http`'s `Hijack` under TinyGo with system:tinygo-netdev. It deadlocks, permanently, on the bodyless GET that every upgrade request is. Use fasthttp's `RequestCtx.Hijack` instead, which is a synchronous handoff with no background read to abort.

```yaml
observed: TinyGo 0.41.1 darwin/arm64, gorilla/websocket 2026-08-10; re-confirmed against fasthttp/websocket
mechanism: >
  net/http's serve calls startBackgroundRead before every bodyless-GET handler
  (server.go:1987). hijackLocked then calls abortPendingRead, which sets the
  read deadline into the past and blocks on cond.Wait() until the background
  read notices. It never notices: netdev's waitFD returns immediately for a
  zero deadline and sysRecv then blocks inside a plain recv(), and the Netdever
  interface takes the deadline by value at call time, so a later
  SetReadDeadline cannot reach a call already in flight.
positive_control: >
  deliver a byte after the request is parsed -- not pipelined, bufio swallows
  that one -- and the background read completes on its own, Hijack returns
  normally, and everything downstream works. That pins the diagnosis on the
  pending read rather than on Hijack itself.
remedy:
  preferred: >
    fasthttp RequestCtx.Hijack. fasthttp finishes the response, stops reading,
    and calls the handler on the same goroutine; there is no background read.
    Verified end to end by fasthttpwebsocket/compat_test.go, which runs its
    whole battery over real sockets under tinygo test.
  nethttp_workarounds: >
    only if net/http is mandatory: replace the server with an accept loop
    (~90 lines, loses keep-alive), or demultiplex in front of it (~130 lines,
    keeps keep-alive, but only inspects the first request on a connection).
    Both were measured working; both are strictly worse than using fasthttp.
scope: any Netdever whose read call takes its deadline by value, which is the interface's shape rather than one implementation's choice
```
