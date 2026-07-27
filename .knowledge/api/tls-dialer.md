---
id: api:tls-dialer
type: api
title: Internal TLS Dialer Seam
---
Unexported single-function seam every backend implements; it is the only boundary where decision:per-os-integration-model differences are visible.

```yaml
signature: func dialTLS(ctx context.Context, host, port string, cfg *Config, timeout time.Duration) (net.Conn, error)
contract:
  returns: a net.Conn whose bytes are already plaintext HTTP
  verification: complete before return; a returned conn is a verified peer
  hostname: host is the SNI name and the name checked against the certificate
  deadlines: returned conn honors SetDeadline; see requirement:deadline-support
  close: releases all native handles exactly once, safe to call twice
  concurrency: dialTLS is safe for concurrent use; a returned conn is not
  errors: always a *Error, per requirement:error-classification
implementations:
  std_go:
    file: roundtrip_std.go
    detail: not used; that path delegates to net/http.Transport wholesale
  darwin:
    file: dial_darwin.go plus tls_darwin.c
    model: connection-owning; nw_connection does DNS, TCP and TLS
    status: shipped
  linux:
    file: dial_linux.go plus tls_mbedtls.c
    model: BIO callback; Go owns the socket, mbedTLS transforms bytes
    status: shipped
  windows:
    file: dial_windows.go
    model: buffer transform in pure Go over a Go-owned socket
    status: provisional
  fallback:
    file: dial_unsupported.go
    detail: returns ErrPlatformNotSupported
rationale: >
  keeping the seam at net.Conn lets api:https-transport stay backend-agnostic
  while each backend still uses its native model
consumed_by: api:https-transport
detail: flow:tls-dial-tinygo
