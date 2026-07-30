---
id: requirement:tls-session-resumption
type: requirement
title: TLS Session Resumption Across Connections
---
When requirement:connection-reuse cannot supply a pooled connection, the new handshake should resume a previous session instead of running a full one, on every backend that offers it.

```yaml
priority: will-not-do on darwin; unevaluated elsewhere
state: >
  measured and rejected for the darwin dial path on 2026-07-30. Not implemented
  on any backend. The rest of this concept is the design that would apply if a
  backend ever shows a gain.
darwin_measurement:
  method: >
    sec_protocol_options_set_tls_resumption_enabled and tls_tickets_enabled
    forced on, forced off, and left at the default, six connections each to a
    real service endpoint with reuse disabled
  result: 89..105 ms in all three modes, indistinguishable
  reading: >
    the framework's session cache is not where the per-connection cost lives;
    see metric:tls-handshake-cost, which rules out DNS as well
  second_reason_not_to: >
    the cache is process-wide and keyed by peer, while verification varies per
    data:https-config. Opting in would let an InsecureSkipVerify connection hand
    a session to a verifying one, which is the cache_key hard rule below being
    violated by a cache this package does not control.
applies_to: the tinygo path only; std go inherits crypto/tls session caching
why_secondary: >
  it saves a round trip and the chain work, not the whole cost in
  metric:tls-handshake-cost, so it only matters on first call, post-idle, and
  after a peer-initiated close
current_state: >
  every backend allocates its context or credential per connection and none
  retains session state, so resumption is impossible by construction today
backends:
  securetransport:
    mechanism: SSLSetPeerID opts the context into the process session cache
    change: set a peer id before handshake; nothing else is retained
    reach: the in-band upgrade path of decision:darwin-hybrid-tls, TLS 1.2 only
  network_framework:
    mechanism: sec_protocol_options resumption and ticket flags, both present in the dyld cache
    change: none; measured as no gain, see darwin_measurement
  openssl:
    mechanism: client session cache on a shared SSL_CTX
    change: >
      hoist SSL_CTX out of the per-connection state, enable SSL_SESS_CACHE_CLIENT,
      and carry the session with SSL_get1_session and SSL_set_session
  schannel:
    mechanism: Schannel caches sessions per credential handle
    change: share one credential handle across connections instead of acquiring one per session
  mbedtls:
    mechanism: mbedtls_ssl_get_session and mbedtls_ssl_set_session, tickets already enabled in the vendored build
    change: retain a session per cache key and restore it before handshake
trust_store_caching:
  applies_to: mbedtls, which re-parses the whole CA bundle per dial
  change: parse once into a shared mbedtls_x509_crt and share it across configs
  independent_of: resumption; it is worth doing even if resumption is not
cache_key:
  must_include: host, port, and every security-relevant field of data:https-config
  rationale: >
    a session carries the peer identity and the negotiated parameters, so
    resuming it under a different configuration would silently reuse a decision
    the new configuration did not make
  hard_rule: >
    a session established with verification skipped is never offered to a
    verifying dial, and a session established without a client certificate is
    never offered to a dial that presents one
early_data:
  rule: TLS 1.3 0-RTT must not be enabled
  reason: >
    early data is replayable, and the driving workload is non-idempotent writes
    over POST
acceptance:
  - resumption is measured per backend against the baseline in metric:tls-handshake-cost, and a backend that shows no gain is left alone
  - measuring openssl, schannel and mbedtls needs a linux or windows runtime, which requirement:platform-matrix does not yet have
  - a resumed connection is still a verified peer under rule:certificate-verification-default
  - changing any security-relevant config field forces a full handshake
  - cached state is bounded and released on Close, per the handle contract in api:tls-dialer
  - the platform states in requirement:platform-matrix are unchanged by this work
decided_by: decision:handshake-cost-mitigation
