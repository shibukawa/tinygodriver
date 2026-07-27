---
id: decision:v1-scope
type: decision
title: v1 Scope Boundary
---
v1 ships a TLS client only: `net/http`-compatible request helpers, a `RoundTripper`, and client TLS configuration.

```yaml
state: accepted
in_scope:
  - api:https-functions
  - api:https-transport
  - data:https-config
  - requirement:tls-client-config
partial:
  client_certificates: >
    in scope, and delivered everywhere except darwin, which refuses them
    explicitly. See requirement:tls-client-config.
out_of_scope:
  server_tls:
    reason: needs backend server mode, cert/key loading, and ALPN negotiation on three stacks
    defer_to: v2
  connection_pool:
    reason: keep-alive plus native session lifetime doubles state management
    defer_to: v2
    v1_behavior: one TLS connection per request, closed after response body read
  http2:
    reason: ALPN h2 requires a second protocol implementation
    defer_to: unplanned
rationale: >
  Client path is the blocking gap for tinygo desktop apps. Server TLS is already
  reachable by terminating TLS in front of the process.
