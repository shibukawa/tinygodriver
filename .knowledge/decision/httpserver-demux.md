---
id: decision:httpserver-demux
type: decision
title: Demultiplex Upgrades in Front of http.Server
---
Keep the real `http.Server` and divert only upgrade requests to a hijackable path, instead of replacing the server wholesale. One listener, one port; WebSocket stays one endpoint among many.

```yaml
state: accepted; implemented as httpserver/
drives: requirement:httpserver-package
mechanism: >
  accept, read the request head, parse it with http.ReadRequest over a
  bytes.Reader. On bypass, call the handler directly over a ResponseWriter that
  implements Hijacker. Otherwise hand the connection to a real http.Server
  through a channel-fed net.Listener, with the head replayed via io.MultiReader
why_not_replace_server: >
  a hand-written accept loop loses everything http.Server provides. Measured:
  keep-alive gone, one request per connection. Timeouts, Expect: 100-continue,
  chunked responses and graceful shutdown all become ours to write. The
  prototype that did this is discarded
why_not_fix_netdev: not possible, see rule:nethttp-hijack-deadlock
open_question_resolved:
  upgrade_on_reused_connection:
    problem: >
      only the first request on a connection is inspected, so an upgrade
      arriving as a later request on a keep-alive connection reaches
      http.Server and deadlocks
    accepted: >
      browsers open a fresh connection for a WebSocket handshake, so first-request
      inspection holds in practice. Inspecting every request would mean replacing
      http.Server, which this decision rejects
    mitigation: >
      wrap only the handler given to http.Server with a guard that answers 501
      to an upgrade request. The bypass path calls the unwrapped handler, so the
      guard cannot fire there. Turns a silent hang into a clear response
  head_read_timeout:
    problem: reading the head with no deadline leaves a slowloris hole
    resolved: >
      set the deadline before the read and clear it after handoff. This is the
      shape rule:tinygo-threads-scheduler requires anyway, and it is exactly what
      net/http cannot do here
decoupled_from_websocket: >
  the bypass predicate defaults to "Connection contains the upgrade token", not
  to a websocket-specific check, so the package does not import
  system:gorilla-websocket and also covers h2c
host_go_policy: >
  std path delegates straight to http.Server with no head-reading layer, per
  requirement:std-go-delegation. The demux works on host Go too, measured 27/27,
  but plain http.Server is strictly better there
cost: >
  one extra read and one buffer copy per connection, on the first request only
sunset: >
  the package becomes unnecessary if tinygo net makes deadlines live or tinygo
  net/http drops the background read; say so in the package doc
verified_as_prototype: >
  2026-08-10, tinygo 0.41.1 darwin/arm64. 27/27 including HTTP keep-alive, from
  both a tinygo and a host-Go client. Plain http.Server fails at the handshake;
  the replace-the-server prototype scores 26/27, losing keep-alive
```
