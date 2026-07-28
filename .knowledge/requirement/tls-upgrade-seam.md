---
id: requirement:tls-upgrade-seam
type: requirement
title: TLS Must Attach to an Already-Connected Socket
---
Delivered: a call that wraps a live plaintext socket in TLS and returns a plaintext-carrying `net.Conn`, which is what an in-band upgrade like PostgreSQL's SSLRequest needs.

```yaml
priority: must
state: satisfied and exported on darwin and linux, 2026-07-28
exported_api: api:tls-upgrade
internal_shape:
  signature: func upgradeTLS(fd int, host string, cfg *Config, timeout time.Duration) (net.Conn, error)
  darwin: dial_darwin.go, Secure Transport over the fd, see decision:darwin-hybrid-tls
  linux: dial_mbedtls.go, mbedtls_ssl_set_bio over the fd
  windows: not implemented; upgrade_unsupported.go returns ErrPlatformNotSupported
fd_constraint_why:
  problem: >
    tinygo net.TCPConn.SyscallConn returns "SyscallConn not implemented", so a
    descriptor cannot be recovered from a net.Conn on the tinygo path
  resolution: the seam takes an int fd directly rather than a net.Conn
  postgres_route: >
    pgx Config.DialFunc is public, so the driver supplies a conn carrying its fd
    without widening the fork patch of decision:postgres-backend-split
contract:
  returns: a net.Conn whose bytes are plaintext
  verification: complete before return, identical to the dial path
  host: used for both SNI and the name checked against the certificate
  errors: requirement:error-classification
consumer: decision:postgres-tls-via-proxy, at pgconn startTLS
open:
  windows: >
    system:schannel is a buffer transformer and Go owns the socket, so the model
    fits; only the implementation is missing
  export_decision: >
    whether to export this, and in what shape, is settled by the postgres driver
    as first consumer
