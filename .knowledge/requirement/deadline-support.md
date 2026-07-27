---
id: requirement:deadline-support
type: requirement
title: Deadlines and Cancellation
---
The `net.Conn` from api:tls-dialer must honor `SetDeadline`, and dialing must observe the request `Context`.

```yaml
priority: must
reason: >
  http.Client timeouts and Request.Context are implemented through conn
  deadlines; a backend that ignores them hangs forever on a stalled peer
per_backend:
  darwin:
    mechanism: >
      no deadline on nw_connection, so each operation waits on a dispatch
      semaphore with a timeout derived from the deadline
    caveat: a blocked receive needs nw_connection_cancel to unblock
    status: implemented, with a 5 minute default cap per operation
  linux:
    mechanism: >
      Go owns the socket, so set SO_RCVTIMEO and SO_SNDTIMEO, or poll before
      the BIO callback reads
    caveat: >
      a timed-out mbedtls_ssl_read returns WANT_READ, so the retry loop must
      check the deadline itself or it spins forever
    status: to implement
  windows:
    mechanism: the deadline lives on the Go-owned socket; the native layer is stateless
    caveat: none, this is the simplest of the three
    status: provisional
tinygo_limitation:
  detail: >
    tinygo's net/http removes setRequestCancel, so http.Client.Timeout never
    reaches a custom RoundTripper and is silently ignored. Its own Transport is
    honored, only the timeout plumbing is missing.
  workaround: a request context deadline, or Transport.DialTimeout and ResponseTimeout
  duty: document it; the package cannot fix it from outside net/http
acceptance:
  - a request context deadline of 100ms against a non-responding TLS server
    returns within ~100ms on every compiler
  - a cancelled request Context aborts an in-progress handshake
  - the returned error satisfies net.Error with Timeout() true
  - no operation can block indefinitely, even with no deadline set
applies_to: api:tls-dialer
