---
id: decision:dynamodb-connection-policy
type: decision
title: Pool Settings Belong To The Client, The Pool Does Not
---
`nosql/dynamodb` configures the shared transport pool for a single-host RPC workload and adds no connection state of its own.

```yaml
state: accepted
accepted_on: 2026-07-31
implemented: >
  WithMaxIdleConns and Close on the client, forwarded through
  aws.NewHTTPClient to https.Transport or net/http.Transport
context: >
  requirement:connection-reuse shipped, so the cost that shaped the earlier
  version of this decision is gone: 87-110ms per call became 11-12ms. What
  remains is choosing the settings, because the transport defaults are tuned for
  a general client and this workload is one host, many small requests.
measurement: requirement:dynamodb-driver-validation
settings:
  MaxIdleConnsPerHost:
    default_in_transport: 2
    chosen_here: 4, exposed as WithMaxIdleConns
    reason: >
      every request goes to one host, so the per-host cap is the whole pool for
      this client. Measured: with the cap at 2, four concurrent calls produced
      two pooled hits at 15-17ms and two fresh handshakes at 94-97ms.
    guidance: a caller running N concurrent operations should set it to N
  IdleConnTimeout:
    chosen_here: the transport default, 20s, not raised
    reason: >
      AWS endpoints idle out around 60s, so 30-50s would keep more connections
      warm. It is refused because a stale entry is recovered by replaying the
      request, and replay is exactly what a non-idempotent UpdateItem cannot
      afford; see requirement:dynamodb-retry-policy.
    consequence: >
      an application calling less than once per 20s pays a handshake every time.
      Measured 141ms after a 21s idle, then 11ms once warm again.
  DisableKeepAlives: never set by the client; reachable through WithHTTPClient
client_api:
  - "func (c *Client) Close() error, which calls CloseIdleConnections"
  - WithMaxIdleConns(n int), forwarded to the transport on both build paths
  - >
    a client built with WithHTTPClient is left alone by Close, since its owner
    may still be using it
  - >
    tested by counting the connections a test server accepts: three sequential
    calls take one, and a call after Close takes a second
  - >
    Close is required rather than cosmetic: pooled native TLS handles outlive
    the last request otherwise, per the handle contract in api:tls-dialer
no_pool_here: >
  the client keeps no connection state. One pool implementation, in
  api:https-transport, is what decision:handshake-cost-mitigation settled, and a
  second one above it would be invisible to CloseIdleConnections.
batching_reframed: >
  BatchGetItem and BatchWriteItem stay in requirement:dynamodb-client-scope for
  what they always were, fewer round trips and fewer write units. They are no
  longer a handshake mitigation, which is how the earlier draft justified them.
documented_in:
  nosql/dynamodb README: >
    a "Connections" section: the two defaults, when to raise WithMaxIdleConns,
    and why Close matters. The measured numbers stay in the https README, which
    owns the transport; this one links to it rather than repeating it.
  godoc:
    - Client.Close, stating that pooled TLS handles outlive the last request
    - WithMaxIdleConns, stating the rule of thumb, set it to your concurrency
  not_documented: >
    why IdleConnTimeout is not raised toward the AWS 60s idle timeout. That is
    reasoning about a rejected option, so it stays here.
supersedes: >
  the "one TLS connection per call, measured and accepted" decision drafted on
  2026-07-30, which decision:v1-scope and requirement:connection-reuse overtook
  the following day
related: metric:tls-handshake-cost
