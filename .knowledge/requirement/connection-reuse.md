---
id: requirement:connection-reuse
type: requirement
title: Connection Reuse in the Native Transport
---
api:https-transport must keep an idle TLS connection per destination and reuse it for the next request to the same destination, so the handshake in metric:tls-handshake-cost is paid once per connection rather than once per request.

```yaml
priority: must
state: shipped 2026-07-30, files pool_native.go and roundtrip_native.go
applies_to: the tinygo path only; std go already pools inside net/http.Transport per requirement:std-go-delegation
pool_key: scheme, host, port, and the effective proxy from requirement:http-proxy-support
api_surface:
  added_to_transport:
    - MaxIdleConnsPerHost int, zero means 2
    - IdleConnTimeout time.Duration, zero means 20s
    - DisableKeepAlives bool, which restores the one-shot behavior
    - CloseIdleConnections(), on both build paths
  naming: mirror net/http.Transport field names, per requirement:nethttp-compatible-client
  std_path: the same three fields are forwarded to the net/http.Transport
  no_new_exported_types: the pool stays unexported
  total_cap: 32 idle connections across all hosts, oldest evicted first, unexported
reuse_eligibility:
  reusable_only_if:
    - response body was read to EOF, not abandoned mid-stream
    - response framing was known, so Content-Length or chunked, not close-delimited
    - neither request nor response carried Connection: close
    - no error and no cancellation touched the connection
  request_change: >
    stop forcing Close on the outbound request; the native path currently sets
    it because there is no reuse
  buffered_reader: >
    the bufio.Reader wrapping the conn must be pooled with the conn. Discarding
    it drops bytes already read past the response body.
  deadline_hygiene: >
    a conn returned to the pool must have its deadline cleared, and the next
    lease must set a fresh one; a leftover deadline would fail the next request
    with a spurious timeout under requirement:deadline-support
  cancellation: >
    the watcher that closes the conn on context cancellation must evict it from
    the pool, never return it
stale_connection_recovery:
  problem: >
    a pooled connection can be closed by the peer while idle, and the native
    backends give no readable signal without a read
  rules:
    - expire lazily on lease using IdleConnTimeout, not with a reaper goroutine
    - retry once on a fresh connection when a leased conn fails before any response byte arrives
    - replay only when the request body is absent or GetBody can rebuild it
    - never retry once any response byte has been read
  default_idle_timeout: >
    20s, chosen to sit under the 60s idle timeout common to AWS service
    endpoints and load balancers. net/http's 90s is safe only because it can
    check a pooled connection before reusing it.
  replay_evidence: >
    the byte count of the response read so far, kept on the connection and
    reset per request. It deliberately ignores the connection's broken flag:
    by the time a failure is judged the connection has already been closed, so
    broken says nothing about whether the server acted.
constraints:
  no_background_goroutine: >
    rule:tinygo-threads-scheduler forbids relying on a goroutine making progress
    while another blocks in system:tinygo-netdev, so pool maintenance happens on
    the lease and release paths
  concurrency: >
    Transport is documented safe for concurrent use, so the pool must be too,
    while a leased conn stays owned by one request
  bounded_state: >
    idle connections are capped per host and in total; an embedded target must
    not accumulate native TLS handles
  handle_release: >
    an evicted conn is closed exactly once, releasing backend handles per the
    close contract in api:tls-dialer
acceptance:
  verified_by: >
    pool_test.go, which counts the connections the test server accepts. It
    carries no build tag, so every case below is asserted on the native path and
    on std go, and any divergence from net/http fails the suite.
  cases:
    - three sequential requests to one host accept one connection
    - a Connection: close response is not reused
    - a body abandoned before EOF closes its connection instead of pooling it
    - HEAD and 204 are reused even when the caller never reads the body
    - an expired IdleConnTimeout entry is not reused
    - a request cancelled mid-flight leaves no reusable connection behind
    - a peer that closes an idle connection produces a transparent retry, including for a POST with a body
    - a deadline from one request does not reach the next on the same connection
    - DisableKeepAlives accepts one connection per request
  also_verified:
    - the whole https suite passes on both paths and under -race
    - tinygo 0.41.1 builds the package for darwin arm64
    - measured end to end, see after_connection_reuse in metric:tls-handshake-cost
backend_independence:
  by_construction: >
    pool_native.go carries no OS build tag and touches only net.Conn plus
    dialTLSMaybeProxy, which every entry in requirement:platform-matrix
    provides. Stale-connection recovery keys on the response byte count rather
    than on a backend-specific error value, so no backend needs to report a
    peer close in any particular way.
  verified_per_backend:
    network_framework: full pool suite on darwin arm64, plus the endpoint measurement
    mbedtls:
      darwin: full pool suite under -tags darwinstarttlswith13
      linux_arm64: full pool suite and the whole https suite, in a container
      linux_amd64: full pool suite, same container image under emulation
    schannel: cross-compiles under mingw-w64; never run, as requirement:windows-tinygo-feasibility already records
  not_verified:
    windows_runtime: unchanged gap, see requirement:windows-tinygo-feasibility
    linux_throughput: >
      the amd64 run was emulated, so it is evidence of correctness and not of
      speed. requirement:platform-matrix already wants a native amd64 job.
detail: flow:connection-lease
decided_by: decision:handshake-cost-mitigation
