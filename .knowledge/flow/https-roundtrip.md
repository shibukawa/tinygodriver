---
id: flow:https-roundtrip
type: flow
title: HTTPS Round Trip
---
One request from `https.Get` to a parsed `*http.Response` on the TinyGo path.

```yaml
flow:
  - id: call
    actor: application
    action: https.Get(url)
    next: client
  - id: client
    actor: api:https-functions
    action: DefaultClient.Get, which applies redirect and cookie policy
    next: roundtrip
  - id: roundtrip
    actor: api:https-transport
    action: RoundTrip(req)
    next: dial
  - id: dial
    actor: api:tls-dialer
    action: dialTLS(req.Context(), host, port, cfg)
    detail: flow:tls-dial-tinygo
    on_error: return classified error per requirement:error-classification
    next: deadline
  - id: deadline
    actor: api:https-transport
    action: conn.SetDeadline from req context and Transport timeouts
    next: write
  - id: write
    actor: api:https-transport
    action: req.Write(conn), which serializes request line, headers, and body
    next: read
  - id: read
    actor: api:https-transport
    action: http.ReadResponse(bufio.NewReader(conn), req)
    next: wrap
  - id: wrap
    actor: api:https-transport
    action: replace resp.Body with a reader that closes conn on Close
    next: done
  - id: done
    actor: application
    action: read body, then Close, releasing the TLS connection
notes:
  - the dial and done steps are now flow:connection-lease, which reuses a pooled connection when one is available
  - std go replaces steps dial through read with net/http.Transport
