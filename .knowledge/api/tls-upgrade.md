---
id: api:tls-upgrade
type: api
title: Exported In-Band TLS Upgrade
---
Public pair for protocols that negotiate in plaintext and then switch the same socket to TLS: PostgreSQL SSLRequest, MySQL, SMTP and IMAP STARTTLS.

```yaml
signatures:
  dial: func DialPlain(ctx context.Context, host, port string) (net.Conn, error)
  upgrade: func Upgrade(ctx context.Context, conn net.Conn, host string, cfg *Config) (net.Conn, error)
  carrier: type UpgradableConn interface { net.Conn; Fd() int }
  sentinel: ErrNotUpgradable
why_a_pair: >
  Upgrade alone is unusable on tinygo, because net.TCPConn.SyscallConn returns
  an error there and no descriptor can be recovered from an arbitrary net.Conn.
  DialPlain returns a connection that carries its own descriptor, so portable
  code has a way to produce an upgradable connection
implementations:
  std_go:
    file: upgrade_std.go
    dial: net.Dialer.DialContext
    upgrade: tls.Client plus HandshakeContext; accepts any net.Conn
  native:
    file: upgrade_native.go, tags (tinygo || force_tinygo_logic) && (darwin || linux)
    dial: netdev socket wrapped in plainConn, which implements UpgradableConn
    upgrade: takes Fd() and calls the internal upgradeTLS of requirement:tls-upgrade-seam
  unsupported:
    file: upgrade_unsupported.go
    detail: both return ErrPlatformNotSupported; never a plaintext fallback
contract:
  host: SNI and the verified name; Config.ServerName overrides it
  verification: complete before return, identical to api:tls-dialer
  ownership:
    success: the returned conn owns the descriptor; the caller must not close the input
    failure: the input is untouched and still usable, so plaintext fallback stays possible
  timeout: context deadline when set, otherwise defaultOpTimeout
  errors: same sentinels as requirement:error-classification on both paths
ownership_mechanism: >
  plainConn.release() makes its Close a no-op once Upgrade succeeds, so a
  deferred Close on the plaintext conn cannot close a descriptor the TLS
  connection still uses
consumer: decision:postgres-tls-via-proxy
address_preservation: >
  the returned conn reports the pre-upgrade LocalAddr and RemoteAddr. The
  backends build their conn from a bare descriptor and cannot recover the port,
  and a RemoteAddr without one breaks any caller that reconnects to the same
  server. PostgreSQL's CancelRequest does exactly that, and silently stopped
  cancelling until this was fixed
deadline_contract: >
  SetDeadline never blocks behind an in-flight Read. The native conns keep their
  deadlines under a lock separate from the one serializing session I/O, because
  a caller sets a deadline precisely when a read is blocked. A single lock made
  a cancellation watcher stall behind the query it was cancelling
verified:
  tests: upgrade_test.go runs on std go, force_tinygo_logic, and darwinstarttlswith13
  e2e: identical source completed a plaintext-then-upgrade GET under go and tinygo
  first_consumer: >
    database/sql/pgxstdlib drives sslmode through this seam, including
    verify-full with a custom root and cancellation over TLS
