---
id: decision:handshake-cost-mitigation
type: decision
title: How the Per-Request Handshake Cost Is Removed
---
Ranking of the ways to avoid the cost in metric:tls-handshake-cost, and which ones this repository takes.

```yaml
state: accepted
accepted_on: 2026-07-30
supersedes_part_of: decision:v1-scope, the connection_pool out_of_scope entry
options:
  connection_reuse:
    effect: removes the handshake for every request after the first to a host
    cost: pool state, reuse-eligibility rules, and stale-connection recovery
    scope: api:https-transport only, backend-agnostic
    verdict: shipped; see requirement:connection-reuse
  session_resumption:
    effect: >
      abbreviated handshake when the pool misses, so it saves a round trip and
      the certificate chain work, not the whole cost
    cost: one backend-specific change per entry in requirement:platform-matrix
    verdict: >
      rejected on darwin, where it measured no gain at all and would carry a
      real cache-scope risk; unmeasurable on the other backends from a darwin
      host. See requirement:tls-session-resumption.
  trust_store_caching:
    effect: >
      removes repeated CA parsing per dial, which is a pure waste independent of
      both options above and largest on system:mbedtls
    verdict: accepted, folded into requirement:tls-session-resumption
  connection_prewarm:
    effect: >
      moves the first-call cost off the critical path by dialing before the
      first request
    verdict: >
      deferred; it is a caller-side pattern once requirement:connection-reuse
      exists, so no API is added for it
  http2:
    effect: multiplexing would also remove per-request setup
    verdict: rejected, still unplanned per decision:v1-scope
  caller_side_batching:
    effect: fewer requests, unchanged per-request cost
    verdict: >
      out of scope; batching is the calling client's design choice, not a
      transport capability
rationale: >
  reuse and resumption looked complementary, one winning the steady state and
  the other covering the transitions. Measurement collapsed that: reuse took
  90 ms to 10 ms, and resumption moved nothing, because the per-connection cost
  on darwin is connection establishment rather than the key exchange.
resolved_question: >
  the first-call excess was chased and is not a revocation fetch, not DNS, and
  not a missing session cache. metric:tls-handshake-cost records all three
  negative results. It is intrinsic to opening an nw_connection, which leaves
  not opening one as the only lever.
