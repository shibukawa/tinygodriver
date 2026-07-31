---
id: metric:tls-handshake-cost
type: metric
title: Per-Request TLS Handshake Cost
---
Measured cost of the one-connection-per-request behavior fixed by decision:v1-scope, and the workload class where it dominates.

```yaml
measured_on: 2026-07-30
measured_with: tinygo 0.41.1, darwin arm64, api:https-transport native path
workload: 5 sequential POSTs to dynamodb.ap-northeast-1.amazonaws.com
observed:
  first_call_ms: 499
  steady_state_ms: 87..110
  breakdown: >
    dominated by dial plus handshake; the DynamoDB GetItem it carries is
    sub-millisecond server-side
  first_call_excess: >
    ~400 ms above steady state, unattributed; candidates are trust store load,
    revocation fetch, and lazy backend initialization
amortization:
  amortized: >
    object transfer, so requirement:s3-client-scope operations tolerate it; one
    handshake per multi-megabyte body is noise
  not_amortized: >
    small request-response RPC over HTTPS, where the handshake is one to two
    orders of magnitude above the useful work
per_connection_setup_confirmed_in_source:
  network_framework: nw_parameters_create_secure_tcp and nw_connection_create per dial
  securetransport: SSLCreateContext per session, and SSLSetPeerID is never called
  openssl: SSL_CTX_new per connection state
  schannel: AcquireCredentialsHandleW per session, freed with it
  mbedtls: >
    mbedtls_ssl_config_init per session, and mbedtls_x509_crt_parse re-parses
    the whole CA bundle on every dial
after_connection_reuse:
  measured_on: 2026-07-30, same binary, same endpoint, six sequential POSTs
  first_call_ms: 121..151
  steady_state_ms: 10..12
  reading: >
    the residual is the round trip itself, so requirement:connection-reuse
    removed essentially all of the avoidable cost
  control: >
    the same run with DisableKeepAlives set stayed at 89..105 ms, which is the
    v1 behavior reproduced on demand
  concurrency_note:
    measured_on: 2026-07-31, same endpoint, four goroutines per wave
    result: >
      the second wave produced two pooled calls at 15..17 ms and two fresh
      handshakes at 94..97 ms, because MaxIdleConnsPerHost defaults to 2
    reading: >
      the default suits a client spreading requests over hosts. A single-host
      RPC workload should raise it to its concurrency, which
      decision:dynamodb-connection-policy does.
  post_idle:
    measured_on: 2026-07-31
    result: 141 ms on the first call after 21 s idle, then 11 ms
    reading: IdleConnTimeout expiring on lease, as designed
attribution_of_the_per_connection_cost:
  network_rtt_ms: ~10, from the reuse steady state
  session_resumption: >
    not a factor. Forcing sec_protocol_options_set_tls_resumption_enabled and
    tls_tickets_enabled on, and forcing both off, each measured the same 90 ms
    as the default. See requirement:tls-session-resumption.
  dns: >
    not a factor. Dialing a pre-resolved IP with an SNI override measured
    90..104 ms against 89..105 ms by name.
  conclusion: >
    the ~90 ms is nw_connection establishment plus the handshake itself and is
    not reachable from the caller. The only lever is not opening the connection.
documented_in: >
  https README, section "Connection reuse". The concurrency result and the
  replay caveat were added there on 2026-07-31; the steady-state numbers were
  already there.
drives:
  - decision:handshake-cost-mitigation
  - requirement:connection-reuse
  - requirement:tls-session-resumption
