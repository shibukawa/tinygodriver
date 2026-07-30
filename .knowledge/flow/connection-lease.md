---
id: flow:connection-lease
type: flow
title: Connection Lease and Release
---
How one request under requirement:connection-reuse takes a connection from the pool and decides whether to give it back.

```yaml
flow:
  - id: lease
    actor: api:https-transport
    action: look up the pool by scheme, host, port and effective proxy
    branch:
      hit: expire
      miss: dial
  - id: expire
    actor: api:https-transport
    action: drop the entry if idle longer than IdleConnTimeout, then close it
    branch:
      expired: dial
      fresh: reset
  - id: dial
    actor: api:tls-dialer
    action: dialTLS, paying the handshake in metric:tls-handshake-cost
    detail: flow:tls-dial-tinygo
    on_error: return a classified error per requirement:error-classification
    next: reset
  - id: reset
    actor: api:https-transport
    action: set a fresh deadline and arm the cancellation watcher
    next: write
  - id: write
    actor: api:https-transport
    action: write the request, without forcing Connection close
    on_error: recover
    next: read
  - id: read
    actor: api:https-transport
    action: http.ReadResponse over the bufio.Reader carried with the conn
    on_error: recover
    next: wrap
  - id: recover
    actor: api:https-transport
    action: >
      close the conn; retry once from dial when the conn was leased, no response
      byte arrived, and the body is absent or GetBody can rebuild it
    otherwise: return a classified error
  - id: wrap
    actor: api:https-transport
    action: replace resp.Body with a reader that runs release on Close
    next: done
  - id: done
    actor: application
    action: read the body, then Close
    next: release
  - id: release
    actor: api:https-transport
    action: clear the deadline, stop the watcher, and decide the conn's fate
    branch:
      reusable: return to the pool, subject to the per-host cap
      otherwise: close, releasing backend handles per api:tls-dialer
reusable_when: see the reuse_eligibility rules in requirement:connection-reuse
notes:
  - no reaper goroutine; expiry happens on lease, per rule:tinygo-threads-scheduler
  - a pool miss may still resume a session under requirement:tls-session-resumption
  - std go never enters this flow, per requirement:std-go-delegation
