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
    state: no longer deferred, shipped as requirement:connection-reuse
    original_reason: keep-alive plus native session lifetime doubles state management
    why_it_changed: >
      the deferral was priced for object transfer, which amortizes the
      handshake. Small-RPC workloads do not; metric:tls-handshake-cost measured
      the difference and decision:handshake-cost-mitigation reopened it.
    former_v1_behavior: >
      one TLS connection per request, closed after the response body was read.
      Still reachable with Transport.DisableKeepAlives.
  http2:
    reason: ALPN h2 requires a second protocol implementation
    defer_to: unplanned
rationale: >
  Client path is the blocking gap for tinygo desktop apps. Server TLS is already
  reachable by terminating TLS in front of the process.
